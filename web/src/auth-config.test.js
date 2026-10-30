import test from 'node:test'
import assert from 'node:assert/strict'
import { createAuthHeaders } from './auth-config.js'
import { createRequest } from './request.js'
import { createFileRequest } from './download.js'

const json = (data) =>
  new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } })
const file = () =>
  new Response('synthetic', {
    headers: {
      'Content-Type': 'text/csv',
      'Content-Disposition': 'attachment; filename="synthetic.csv"',
      'Content-Length': '9',
    },
  })

test('API and download share discovery before sending a token through only the custom header', async () => {
  let discoveries = 0,
    requests = 0
  const auth = createAuthHeaders({
    fetcher: async (path, options) => {
      discoveries++
      assert.equal(path, '/api/auth')
      assert.equal(options.headers, undefined)
      assert.equal(options.credentials, 'same-origin')
      assert.equal(options.cache, 'no-store')
      assert.equal(options.redirect, 'error')
      return json({ admin_token_header: 'X-Simharbor-Authorization' })
    },
  })
  const options = {
    getCredential: () => 'SYNTHETIC',
    getAuthHeaders: (value) => auth.get(value),
    onUnauthorized: () => assert.fail('unexpected unauthorized request'),
    fetcher: async (path, options) => {
      requests++
      assert.equal(options.headers.Authorization, undefined)
      assert.equal(options.headers['X-Simharbor-Authorization'], 'Bearer SYNTHETIC')
      assert.equal(options.redirect, 'error')
      return path.endsWith('/export') ? file() : json({ online: false })
    },
  }
  await Promise.all([
    createRequest(options)('/state'),
    createFileRequest(options)('/messages/export'),
  ])
  assert.equal(discoveries, 1)
  assert.equal(requests, 2)
})

test('default header stays Authorization and anonymous requests do not invent a credential', async () => {
  const auth = createAuthHeaders({
    fetcher: async () => json({ admin_token_header: 'Authorization' }),
  })
  assert.deepEqual(await auth.get('SYNTHETIC'), { Authorization: 'Bearer SYNTHETIC' })
  assert.deepEqual(await auth.get(''), {})
})

test('bad or unavailable discovery blocks writes and downloads without a default-header fallback', async () => {
  for (const response of [
    new Response('<html>proxy login</html>'),
    new Response('', { status: 401 }),
    json({ admin_token_header: '' }),
    json({ admin_token_header: 'X-Token\r\nInjected: value' }),
    json({ admin_token_header: 'X-Token', credentials: 'unexpected' }),
    json({ admin_token_header: 'Authorization', skip_admin_token: 'true' }),
    json({ admin_token_header: 'Authorization', skip_admin_token: null }),
  ]) {
    const auth = createAuthHeaders({ fetcher: async () => response })
    let requests = 0
    const options = {
      getCredential: () => 'SYNTHETIC',
      getAuthHeaders: (value) => auth.get(value),
      onUnauthorized: () => {},
      fetcher: () => {
        requests++
        throw new Error('must not send credential')
      },
    }
    await assert.rejects(createRequest(options)('/outgoing/prepare', 'POST', {}))
    assert.equal(requests, 0)
  }
  const auth = createAuthHeaders({
    fetcher: async () => {
      throw new Error('unavailable')
    },
  })
  await assert.rejects(
    createFileRequest({
      getCredential: () => 'SYNTHETIC',
      getAuthHeaders: (value) => auth.get(value),
      onUnauthorized: () => {},
      fetcher: () => assert.fail('download sent without discovery'),
    })('/backups/' + 'a'.repeat(32) + '/download'),
  )
})

test('显式跳过凭据时 API 与下载不发送旧 Token，关闭后恢复原鉴权', async () => {
  let skip = true
  const policies = []
  const auth = createAuthHeaders({
    fetcher: async () => json({ admin_token_header: 'X-Management-Auth', skip_admin_token: skip }),
    onConfig: (config) => policies.push(config.skip_admin_token),
  })
  let requests = 0
  const options = {
    getCredential: () => 'SYNTHETIC-CACHED-TOKEN',
    getAuthHeaders: (value) => auth.get(value),
    onUnauthorized: () => assert.fail('unexpected login prompt'),
    fetcher: async (path, options) => {
      requests++
      assert.deepEqual(options.headers, {})
      return path.endsWith('/export') ? file() : json({ online: false })
    },
  }
  await Promise.all([
    createRequest(options)('/state'),
    createFileRequest(options)('/messages/export'),
  ])
  assert.equal(requests, 2)
  assert.deepEqual(policies, [true])
  skip = false
  auth.reset()
  assert.deepEqual(await auth.get('SYNTHETIC-CACHED-TOKEN'), {
    'X-Management-Auth': 'Bearer SYNTHETIC-CACHED-TOKEN',
  })
  assert.deepEqual(policies, [true, false])
})

test('unauthorized write is not replayed; the next request can discover a changed header', async () => {
  let name = 'Authorization',
    discoveries = 0,
    writes = 0,
    locks = 0
  const auth = createAuthHeaders({
    fetcher: async () => {
      discoveries++
      return json({ admin_token_header: name })
    },
  })
  const api = createRequest({
    getCredential: () => 'SYNTHETIC',
    getAuthHeaders: (value) => auth.get(value),
    onUnauthorized: () => {
      auth.reset()
      locks++
    },
    fetcher: async (_, options) => {
      if (options.method === 'POST') {
        writes++
        name = 'X-New-Auth'
        return new Response('', { status: 401 })
      }
      assert.equal(options.headers.Authorization, undefined)
      assert.equal(options.headers['X-New-Auth'], 'Bearer SYNTHETIC')
      return json({})
    },
  })
  await assert.rejects(api('/outgoing/prepare', 'POST', {}), (e) => e.status === 401)
  assert.equal(writes, 1)
  assert.equal(locks, 1)
  await api('/state')
  assert.equal(discoveries, 2)
  assert.equal(writes, 1)
})

test('credential replaced during discovery cannot send a stale management request', async () => {
  let credential = 'old',
    finish
  const auth = createAuthHeaders({
    fetcher: () =>
      new Promise((resolve) => {
        finish = resolve
      }),
  })
  const api = createRequest({
    getCredential: () => credential,
    getAuthHeaders: (value) => auth.get(value),
    onUnauthorized: () => {},
    fetcher: () => assert.fail('stale credential sent'),
  })
  const pending = api('/settings', 'PUT', {})
  credential = 'new'
  finish(json({ admin_token_header: 'X-Management-Auth' }))
  await assert.rejects(pending)
})

test('failed discovery is not cached permanently and a timeout aborts without replay', async () => {
  let calls = 0
  const auth = createAuthHeaders({
    timeout: 1,
    fetcher: (_, { signal }) => {
      calls++
      if (calls > 1) return Promise.resolve(json({ admin_token_header: 'Authorization' }))
      return new Promise((_, reject) =>
        signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true }),
      )
    },
  })
  await assert.rejects(auth.get('SYNTHETIC'))
  assert.equal(calls, 1)
  assert.deepEqual(await auth.get('SYNTHETIC'), { Authorization: 'Bearer SYNTHETIC' })
  assert.equal(calls, 2)
})
