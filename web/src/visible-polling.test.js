import test from 'node:test'
import assert from 'node:assert/strict'
import { startVisiblePolling, needsInboxRefresh } from './visible-polling.js'

test('隐藏时暂停自动读取，恢复只刷新一次，在途请求不重复且不被中断', async () => {
  const doc = new EventTarget()
  doc.hidden = false
  let callback,
    release,
    calls = 0,
    clears = 0
  const stop = startVisiblePolling(
    () => {
      calls++
      return new Promise((resolve) => {
        release = resolve
      })
    },
    15000,
    {
      document: doc,
      setInterval: (fn) => {
        callback = fn
        return 1
      },
      clearInterval: () => {
        clears++
      },
    },
  )
  callback()
  callback()
  assert.equal(calls, 1)
  doc.hidden = true
  doc.dispatchEvent(new Event('visibilitychange'))
  callback()
  assert.equal(calls, 1)
  doc.hidden = false
  doc.dispatchEvent(new Event('visibilitychange'))
  assert.equal(calls, 1)
  release()
  await new Promise((resolve) => setImmediate(resolve))
  callback()
  assert.equal(calls, 2)
  release()
  stop()
  doc.dispatchEvent(new Event('visibilitychange'))
  callback()
  assert.equal(calls, 2)
  assert.ok(clears >= 2)
})

test('非收件页不读取隐藏短信列表；仍显示的短信详情保留刷新', () => {
  assert.equal(needsInboxRefresh('inbox'), true)
  for (const tab of ['calls', 'recording', 'settings', 'data', 'notifications'])
    assert.equal(needsInboxRefresh(tab), false)
  assert.equal(needsInboxRefresh('calls', true), true)
})
