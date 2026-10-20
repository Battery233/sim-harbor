import test from 'node:test'
import assert from 'node:assert/strict'
import { createSettingsFlow } from './settings-flow.js'
const snapshot = (pending = false, revision = 'a'.repeat(64)) => ({
  values: { log_level: 'info' },
  revision,
  pending_restart: pending,
  credentials_configured: { aes_key: true, aes_iv: true, admin_token: true, bark_key: false },
})
const deferred = () => {
  let resolve, reject
  const promise = new Promise((a, b) => {
    resolve = a
    reject = b
  })
  return { promise, resolve, reject }
}
test('系统配置初次只读，不接受含旧密钥或无效结构的响应', async () => {
  const calls = []
  let data = snapshot()
  const flow = createSettingsFlow({
    request: async (...args) => {
      calls.push(args)
      return data
    },
  })
  assert.equal((await flow.load()).pending_restart, false)
  assert.deepEqual(calls, [['/settings']])
  data = { ...snapshot(), values: { credentials: { aes_key: 'SYNTHETIC' } } }
  await assert.rejects(flow.load())
})
test('保存须确认且只提交一次，结果未知时必须先重新读取', async () => {
  const d = deferred(),
    calls = []
  const flow = createSettingsFlow({
    request: (...args) => {
      calls.push(args)
      return args[1] ? d.promise : Promise.resolve(snapshot())
    },
  })
  await assert.rejects(flow.save({}, {}, 'a'.repeat(64), false))
  assert.equal(calls.length, 0)
  const first = flow.save({ log_level: 'debug' }, { bark_key: 'SYNTHETIC' }, 'a'.repeat(64), true)
  await assert.rejects(flow.save({}, {}, 'a'.repeat(64), true))
  d.reject(new Error('lost response'))
  await assert.rejects(first)
  await assert.rejects(flow.save({}, {}, 'a'.repeat(64), true))
  assert.equal(calls.length, 1)
  await flow.load()
  assert.equal(calls.length, 2)
  assert.equal(calls[0][1], 'PUT')
})
test('保存成功仅标待重启，不自动重启或发短信', async () => {
  const calls = []
  const flow = createSettingsFlow({
    request: async (...args) => {
      calls.push(args)
      return snapshot(true, 'b'.repeat(64))
    },
  })
  const data = await flow.save({ log_level: 'debug' }, {}, 'a'.repeat(64), true)
  assert.equal(data.pending_restart, true)
  assert.equal(calls.length, 1)
  assert.equal(calls[0][0], '/settings')
  assert.equal(calls[0][2].confirmed, true)
  assert.equal(calls[0][2].voicemail_confirmed, false)
})
test('重启须再次确认，丢失响应不补发；恢复后可重新编辑', async () => {
  const calls = []
  let pending = true
  const flow = createSettingsFlow({
    request: async (...args) => {
      calls.push(args)
      if (args[1] === 'POST') throw new Error('unknown')
      return snapshot(pending)
    },
  })
  await assert.rejects(flow.restart('a'.repeat(64), false))
  assert.equal(calls.length, 0)
  await assert.rejects(flow.restart('a'.repeat(64), true))
  await flow.load()
  await assert.rejects(flow.restart('a'.repeat(64), true))
  assert.equal(calls.filter((x) => x[1] === 'POST').length, 1)
  pending = false
  await flow.load()
  await flow.save({}, {}, 'a'.repeat(64), true)
})
test('配置页面关闭后丢弃旧响应，不保存凭据到浏览器存储', async () => {
  const d = deferred(),
    calls = []
  const flow = createSettingsFlow({
    request: (...args) => {
      calls.push(args)
      return d.promise
    },
  })
  const task = flow.save({}, { aes_key: 'SYNTHETIC' }, 'a'.repeat(64), true)
  flow.dispose()
  d.resolve(snapshot(true))
  assert.equal(await task, null)
  await assert.rejects(flow.restart('a'.repeat(64), true))
  assert.equal(calls.length, 1)
})
