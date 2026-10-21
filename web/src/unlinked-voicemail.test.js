import test from 'node:test'
import assert from 'node:assert/strict'
import { createUnlinkedVoicemailFlow, unlinkedAudioPath } from './unlinked-voicemail.js'

const item = (id, file_state = 'available') => ({
  id,
  slot: 0,
  phone: '',
  started_at: 0,
  created_at: 1000,
  size: 7,
  file_name: 'a'.repeat(32) + '.amr',
  file_state,
})
const page = (items, next_before = 0) => ({ items, next_before })

test('未关联录音只读分页；刷新丢掉已关联记录，不保留旧分页', async () => {
  const calls = [],
    views = []
  let response = page(
    Array.from({ length: 50 }, (_, i) => item(60 - i)),
    11,
  )
  const flow = createUnlinkedVoicemailFlow({
    request: async (...args) => {
      calls.push(args)
      return response
    },
    changed: (view) => views.push(view),
  })
  await flow.load()
  response = page([item(10), item(9)])
  await flow.load(true)
  assert.equal(views.at(-1).items.length, 52)
  assert.deepEqual(calls[1], ['/voicemails/unlinked?before=11', 'GET'])
  response = page([item(61), item(59)])
  await flow.load()
  assert.deepEqual(
    views.at(-1).items.map((i) => i.id),
    [61, 59],
  )
  assert.equal(views.at(-1).next, 0)
  assert.equal(await flow.load(true), false)
  assert.equal(calls.length, 3)
  assert.ok(calls.every((c) => c[1] === 'GET'))
})

test('缺失时间、卡槽和号码保留未知；缺失文件不能构造下载地址', async () => {
  let view
  const flow = createUnlinkedVoicemailFlow({
    request: async () => page([item(3, 'missing'), item(2, 'unavailable'), item(1)]),
    changed: (value) => {
      view = value
    },
  })
  await flow.load()
  assert.equal(view.items[0].slot, 0)
  assert.equal(view.items[0].started_at, 0)
  assert.equal(view.items[0].phone, '')
  assert.equal(unlinkedAudioPath(view.items[0]), null)
  assert.equal(unlinkedAudioPath(view.items[1]), null)
  assert.equal(unlinkedAudioPath(view.items[2]), '/voicemails/unlinked/1/audio')
  assert.equal(unlinkedAudioPath({ id: '../private', file_state: 'available' }), null)
})

test('旧响应和离开页面后的响应丢弃，不自动重复请求', async () => {
  const pending = [],
    views = []
  const flow = createUnlinkedVoicemailFlow({
    request: () => new Promise((resolve) => pending.push(resolve)),
    changed: (view) => views.push(view),
  })
  const first = flow.load(),
    second = flow.load()
  pending[1](page([item(2)]))
  await second
  pending[0](page([item(1)]))
  assert.equal(await first, false)
  assert.deepEqual(
    views.at(-1).items.map((i) => i.id),
    [2],
  )
  const last = flow.load()
  flow.dispose()
  pending[2](page([item(3)]))
  assert.equal(await last, false)
  assert.equal(views.length, 1)
})

test('响应异常、错误顺序和危险文件名不进入列表', async () => {
  for (const response of [
    page([item(0)]),
    page([item(1), item(2)]),
    page([{ ...item(1), file_name: '../private' }]),
    page([item(1)], 1),
  ]) {
    let changes = 0
    const flow = createUnlinkedVoicemailFlow({
      request: async () => response,
      changed: () => changes++,
    })
    await assert.rejects(flow.load())
    assert.equal(changes, 0)
  }
})
