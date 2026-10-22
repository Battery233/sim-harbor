import test from 'node:test'
import assert from 'node:assert/strict'
import { deleteRecording } from './voicemail-delete.js'
test('删除录音须明确确认及媒体身份，丢失响应不自动重试', async () => {
  const calls = []
  const request = async (...args) => {
    calls.push(args)
    throw new Error('lost response')
  }
  const id = 'a'.repeat(32)
  await assert.rejects(deleteRecording(request, id, false))
  await assert.rejects(deleteRecording(request, '../unsafe', true))
  assert.equal(calls.length, 0)
  await assert.rejects(deleteRecording(request, id, true), /lost response/)
  assert.deepEqual(calls, [['/voicemails/' + id, 'DELETE', { confirmed: true }]])
})
