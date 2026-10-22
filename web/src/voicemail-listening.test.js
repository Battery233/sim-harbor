import test from 'node:test'
import assert from 'node:assert/strict'
import { createListeningHistory, recordingBadge } from './voicemail-listening.js'
const a = 'a'.repeat(32),
  b = 'b'.repeat(32)
test('组内较早录音也显示标记；全部开始播放后才视为已播放', () => {
  let value
  const storage = {
    getItem: () => value,
    setItem: (_, data) => {
      value = data
    },
  }
  const history = createListeningHistory(storage)
  assert.equal(recordingBadge([], history), '')
  assert.equal(recordingBadge([a, b], history), '📼 未播放')
  history.mark(a)
  assert.equal(recordingBadge([a, b], history), '📼 未播放')
  history.mark(b)
  assert.equal(recordingBadge([a, b], history), '📼 已播放')
  assert.equal(createListeningHistory(storage).has(a), true)
  assert.equal(history.mark('not-a-media-id'), false)
  assert.equal(value.includes('not-a-media-id'), false)
})
test('浏览器禁用存储或损坏数据时仍能在当前页面记忆', () => {
  const history = createListeningHistory({
    getItem() {
      throw new Error('disabled')
    },
    setItem() {
      throw new Error('full')
    },
  })
  assert.equal(history.mark(a), true)
  assert.equal(history.has(a), true)
  assert.equal(createListeningHistory({ getItem: () => '{}' }).has(a), false)
})
