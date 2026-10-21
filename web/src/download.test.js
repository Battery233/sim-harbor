import test from 'node:test'
import assert from 'node:assert/strict'
import { createFileRequest, createDownloadFlow, messageExportQuery } from './download.js'

const response = (body, extra = {}) =>
  new Response(body, {
    headers: {
      'Content-Type': 'text/csv',
      'Content-Disposition': 'attachment; filename="sim-inbox.csv"',
      'Content-Length': String(new TextEncoder().encode(body).length),
      ...extra,
    },
  })
test('未关联录音已关联或文件缺失时给出可操作提示，不自动重发', async () => {
  let count = 0
  for (const error of ['voicemail_unavailable', 'voicemail_audio_missing']) {
    const request = createFileRequest({
      getCredential: () => 'SYNTHETIC',
      onUnauthorized: () => {},
      fetcher: async () => {
        count++
        return new Response(JSON.stringify({ error }), { status: 404 })
      },
    })
    await assert.rejects(
      request('/voicemails/unlinked/1/audio'),
      error === 'voicemail_unavailable' ? /刷新未关联录音/ : /文件缺失或无法读取/,
    )
  }
  assert.equal(count, 2)
})
test('file request uses management credential once and rejects incomplete or unauthorized downloads', async () => {
  let calls = 0,
    locked = 0
  const make = (value) =>
    createFileRequest({
      getCredential: () => 'secret',
      onUnauthorized: () => locked++,
      fetcher: async (path, options) => {
        calls++
        assert.equal(options.headers.Authorization, 'Bearer secret')
        assert.equal(options.method, 'GET')
        return value
      },
    })
  assert.equal(await (await make(response('中文'))('/messages/export')).text(), '中文')
  await assert.rejects(
    make(response('short', { 'Content-Length': '100' }))('/messages/export'),
    /不完整/,
  )
  await assert.rejects(make(new Response('', { status: 401 }))('/messages/export'), /凭据/)
  await assert.rejects(
    make(new Response(JSON.stringify({ error: 'export_limit_exceeded' }), { status: 413 }))(
      '/messages/export',
    ),
    /上限/,
  )
  assert.equal(calls, 4)
  assert.equal(locked, 1)
})
test('file request does not download HTML or a response for a replaced credential', async () => {
  let credential = 'old'
  const request = createFileRequest({
    getCredential: () => credential,
    onUnauthorized: () => {},
    fetcher: async () => {
      credential = 'new'
      return response('file')
    },
  })
  await assert.rejects(request('/messages/export'), /身份/)
  const html = createFileRequest({
    getCredential: () => '',
    onUnauthorized: () => {},
    fetcher: async () => new Response('<html>', { headers: { 'Content-Type': 'text/html' } }),
  })
  await assert.rejects(html('/messages/export'), /类型/)
})
test('AMR download is accepted only when the caller allows that type', async () => {
  const bytes = new TextEncoder().encode('#!AMR')
  const amr = new Response(bytes, {
    headers: {
      'Content-Type': 'audio/amr',
      'Content-Disposition': 'attachment; filename="voicemail-1.amr"',
      'Content-Length': String(bytes.length),
    },
  })
  const request = createFileRequest({
    getCredential: () => 'secret',
    onUnauthorized: () => {},
    fetcher: async () => amr.clone(),
  })
  await assert.rejects(request('/calls/1/voicemail'), /类型/)
  assert.equal(
    await (
      await request('/calls/1/voicemail', 'GET', undefined, {
        types: ['audio/amr'],
        max: 2 * 1024 * 1024,
      })
    ).text(),
    '#!AMR',
  )
})
test('download flow ignores replaced responses and disposed views; failure never saves cached content', async () => {
  const pending = [],
    saved = []
  const flow = createDownloadFlow({
    request: () => new Promise((resolve, reject) => pending.push({ resolve, reject })),
    save: (b, f) => saved.push(f),
  })
  const old = flow.run('/one', 'one'),
    latest = flow.run('/two', 'two')
  pending[0].resolve(new Blob(['one']))
  assert.equal(await old, false)
  pending[1].resolve(new Blob(['two']))
  assert.equal(await latest, true)
  const failed = flow.run('/failed', 'failed')
  pending[2].reject(new Error('failure'))
  await assert.rejects(failed)
  const closed = flow.run('/closed', 'closed')
  flow.dispose()
  pending[3].resolve(new Blob())
  assert.equal(await closed, false)
  assert.deepEqual(saved, ['two'])
})
test('export filters encode exact sender and exclusive date boundary', () => {
  const q = new URLSearchParams(
    messageExportQuery({
      slot: '2',
      phone: '+000',
      from: '2026-10-08T00:00',
      until: '2026-10-09T00:00',
      format: 'csv',
    }),
  )
  assert.equal(q.get('phone'), '+000')
  assert.equal(q.get('slot'), '2')
  assert.equal(Number(q.get('until')) - Number(q.get('from')), 86400)
  assert.throws(() => messageExportQuery({ from: '2026-10-09T00:00', until: '2026-10-08T00:00' }))
  assert.throws(() => messageExportQuery({ slot: '3' }))
})

test('failed backup download makes one confirmed POST and disposed flow cannot start another', async () => {
  let calls = 0
  const request = createFileRequest({
    getCredential: () => 'test',
    onUnauthorized: () => {},
    fetcher: async (path, options) => {
      calls++
      assert.equal(options.method, 'POST')
      assert.deepEqual(JSON.parse(options.body), { confirmed: true })
      return new Response('{}', { status: 503 })
    },
  })
  const flow = createDownloadFlow({ request, save: () => assert.fail('failed file saved') })
  await assert.rejects(flow.run('/backups/download', 'backup.zip', 'POST', { confirmed: true }))
  flow.dispose()
  assert.equal(
    await flow.run('/backups/download', 'backup.zip', 'POST', { confirmed: true }),
    false,
  )
  assert.equal(calls, 1)
})
