import test from 'node:test'
import assert from 'node:assert/strict'
import { createNotificationActions, canRetryNotification } from './notification-flow.js'
test('notification actions require confirmation and never post on initialization', async () => {
  const calls = []
  const flow = createNotificationActions({ request: async (...args) => calls.push(args) })
  assert.equal(calls.length, 0)
  await assert.rejects(flow.test(false))
  await assert.rejects(flow.retry(1, false))
  await assert.rejects(flow.retry(-1, true))
  assert.equal(calls.length, 0)
  await flow.retry(1, true)
  assert.deepEqual(calls[0], ['/notifications/1/retry', 'POST', { confirmed: true }])
})
test('lost test response and concurrent clicks never repeat the request', async () => {
  let finish,
    time = 100,
    calls = 0
  const flow = createNotificationActions({
    now: () => time,
    request: async () => {
      calls++
      await new Promise((resolve) => (finish = resolve))
      throw new Error('lost')
    },
  })
  const first = assert.rejects(flow.test(true))
  await assert.rejects(flow.test(true))
  finish()
  await first
  await assert.rejects(flow.test(true))
  assert.equal(calls, 1)
  time = 160
  const next = assert.rejects(flow.test(true))
  finish()
  await next
  assert.equal(calls, 2)
})
test('accepted, ready and recently requested tasks are not eligible for manual retry', () => {
  const row = { state: 'pending', failures: 1, next_at: 200, manual_retry_at: 0 }
  assert.equal(canRetryNotification(row, 100), true)
  assert.equal(canRetryNotification({ ...row, state: 'sent' }, 100), false)
  assert.equal(canRetryNotification({ ...row, next_at: 99 }, 100), false)
  assert.equal(canRetryNotification({ ...row, manual_retry_at: 90 }, 100), false)
})
