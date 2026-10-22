import test from 'node:test'
import assert from 'node:assert/strict'
import { recordingBadge, markRecordingPlayed } from './voicemail-listening.js'
test('汇总按服务器未播放计数判断，不依赖浏览器本地存储', () => {
  assert.equal(recordingBadge(0, 0), '')
  assert.equal(recordingBadge(2, 1), '📼 未播放')
  assert.equal(recordingBadge(2, 0), '📼 已播放')
})
test('只有成功开始播放后调用写入；失败不自动重试、不伪造成功', async () => {
  const calls = []
  const request = async (...args) => {
    calls.push(args)
    return { played_at: 1234 }
  }
  assert.equal(await markRecordingPlayed(request, 'a'.repeat(32)), 1234)
  assert.deepEqual(calls, [['/voicemails/' + 'a'.repeat(32) + '/played', 'POST', {}]])
  await assert.rejects(markRecordingPlayed(request, 'bad'), /编号/)
  let attempted = 0
  await assert.rejects(
    markRecordingPlayed(async () => {
      attempted++
      throw new Error('offline')
    }, 'b'.repeat(32)),
    /offline/,
  )
  assert.equal(attempted, 1)
  await assert.rejects(
    markRecordingPlayed(async () => ({ played_at: 0 }), 'a'.repeat(32)),
    /未能确认/,
  )
})
