import test from 'node:test'
import assert from 'node:assert/strict'
import { createInboxDeletion } from './inbox-delete.js'
function fixture(api = async (path) => ({ ok: true, id: Number(path.split('/')[2]) })) {
  const state = { target: null, busy: false, error: '' },
    deleted = []
  let identity = 1,
    calls = 0
  const flow = createInboxDeletion({
    state,
    api: (...a) => {
      calls++
      return api(...a)
    },
    session: () => identity,
    onDeleted: (id) => deleted.push(id),
  })
  return { state, flow, deleted, calls: () => calls, switchSession: () => identity++ }
}
test('先展示原短信再确认，取消不调用接口', async () => {
  const f = fixture(),
    m = { id: 3, phone: 'TEST', body: 'SYNTHETIC' }
  assert.equal(await f.flow.confirm(), false)
  f.flow.request(m)
  m.id = 4
  assert.equal(f.state.target.id, 3)
  f.flow.cancel()
  assert.equal(await f.flow.confirm(), false)
  assert.equal(f.calls(), 0)
  f.flow.request({ id: 3 })
  assert.equal(await f.flow.confirm(), true)
  assert.deepEqual(f.deleted, [3])
})
test('处理中不可重复确认或换目标', async () => {
  let release
  const f = fixture(
    () =>
      new Promise((resolve) => {
        release = resolve
      }),
  )
  f.flow.request({ id: 3 })
  const pending = f.flow.confirm()
  assert.equal(f.flow.request({ id: 4 }), false)
  assert.equal(f.flow.cancel(), false)
  assert.equal(await f.flow.confirm(), false)
  assert.equal(f.calls(), 1)
  release({ ok: true, id: 3 })
  await pending
  assert.deepEqual(f.deleted, [3])
})
test('失败不重试也不清除本地短信，失效会话忽略旧响应', async () => {
  const f = fixture(async () => {
    throw new Error('响应未知')
  })
  f.flow.request({ id: 3 })
  assert.equal(await f.flow.confirm(), false)
  assert.equal(f.calls(), 1)
  assert.deepEqual(f.deleted, [])
  assert.equal(f.state.error, '响应未知')
  let release
  const g = fixture(
    () =>
      new Promise((resolve) => {
        release = resolve
      }),
  )
  g.flow.request({ id: 3 })
  const p = g.flow.confirm()
  g.switchSession()
  g.flow.reset()
  release({ ok: true, id: 3 })
  await p
  assert.deepEqual(g.deleted, [])
  assert.equal(g.state.busy, false)
})
test('错误目标或不完整成功响应不能删除界面记录', async () => {
  const f = fixture(async () => ({ ok: true, id: 4 }))
  assert.equal(f.flow.request({ id: -1 }), false)
  f.flow.request({ id: 3 })
  assert.equal(await f.flow.confirm(), false)
  assert.deepEqual(f.deleted, [])
})
