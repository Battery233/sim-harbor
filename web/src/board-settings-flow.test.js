import test from 'node:test'
import assert from 'node:assert/strict'
import { createBoardSettingsFlow } from './board-settings-flow.js'
test('板子设置没有自动查询，写入须确认且请求只一次，未知后只读确认', async () => {
  const calls = []
  const flow = createBoardSettingsFlow({
    request: async (path, method, body) => {
      calls.push(body)
      if (body.action === 'heartbeat_set') throw Error('lost')
      return { seconds: 120 }
    },
  })
  assert.equal(calls.length, 0)
  await assert.rejects(flow.set(120, 110, false))
  assert.equal(calls.length, 0)
  await assert.rejects(flow.set(120, 110, true))
  await flow.set(120, 110, true)
  assert.equal(calls.length, 1)
  await flow.query('heartbeat_query')
  assert.equal(calls[1].action, 'heartbeat_query')
  assert(!calls[1].confirmed)
})
test('板子设置结果不完整不声称成功，不支持任意命令', async () => {
  const flow = createBoardSettingsFlow({ request: async () => ({ seconds: 120 }) })
  await assert.rejects(flow.query('restart'))
  await assert.rejects(flow.query('storage_query'))
  await assert.rejects(flow.set(120, 110, true))
  assert.equal(await flow.set(120, 110, true), null)
})
