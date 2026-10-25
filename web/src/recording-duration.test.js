import test from 'node:test'
import assert from 'node:assert/strict'
import { createRecordingDuration, recordingDurationLabel } from './recording-duration.js'

test('总时长使用音频毫秒数，整秒与不足一秒都清楚展示', () => {
  assert.equal(recordingDurationLabel(10000), '总时长 10 秒')
  assert.equal(recordingDurationLabel(10240), '总时长 10.24 秒')
  assert.equal(recordingDurationLabel(20), '总时长 0.02 秒')
  for (const ms of [undefined, null, 0, -20, NaN, Infinity, '10000', 20.5])
    assert.equal(recordingDurationLabel(ms), '总时长未知')
})

test('相同录音只查询一次，不下载、不写入播放状态', async () => {
  const calls = [],
    labels = []
  const duration = createRecordingDuration({
    request: async (...args) => {
      calls.push(args)
      return { duration_ms: 10000 }
    },
    changed: (label) => labels.push(label),
  })
  const media = 'a'.repeat(32)
  await duration.load(media)
  await duration.load(media)
  assert.deepEqual(calls, [['/voicemails/duration/' + media, 'GET']])
  assert.deepEqual(labels, ['正在读取时长…', '总时长 10 秒'])
  duration.dispose()
  await duration.load('b'.repeat(32))
  assert.equal(calls.length, 1)
})

test('缺失、损坏和查询失败显示未知，失败不自动重试', async () => {
  for (const result of [null, {}, { duration_ms: null }, new Error('synthetic offline')]) {
    const labels = []
    let calls = 0
    const duration = createRecordingDuration({
      request: async () => {
        calls++
        if (result instanceof Error) throw result
        return result
      },
      changed: (label) => labels.push(label),
    })
    await duration.load('../invalid')
    assert.equal(calls, 0)
    const media = 'a'.repeat(32)
    await duration.load(media)
    await duration.load(media)
    assert.equal(calls, 1)
    assert.equal(labels.at(-1), '总时长未知')
  }
})

test('切换录音和离开后的迟到结果不会覆盖当前时长', async () => {
  const labels = [],
    finish = []
  const duration = createRecordingDuration({
    request: () => new Promise((resolve) => finish.push(resolve)),
    changed: (label) => labels.push(label),
  })
  const old = duration.load('a'.repeat(32))
  const current = duration.load('b'.repeat(32))
  finish[1]({ duration_ms: 20000 })
  await current
  finish[0]({ duration_ms: 10000 })
  await old
  assert.equal(labels.at(-1), '总时长 20 秒')
  const pending = duration.load('c'.repeat(32))
  duration.dispose()
  const count = labels.length
  finish[2]({ duration_ms: 30000 })
  await pending
  assert.equal(labels.length, count)
})

test('只有手动刷新会重试读取失败的时长，成功后复用且不重叠请求', async () => {
  let calls = 0,
    finish
  const labels = []
  const duration = createRecordingDuration({
    request: async () => {
      calls++
      if (calls === 1) throw new Error('synthetic offline')
      return new Promise((resolve) => (finish = resolve))
    },
    changed: (label) => labels.push(label),
  })
  const media = 'a'.repeat(32)
  await duration.load(media)
  await duration.load(media)
  assert.equal(calls, 1)
  const retried = duration.load(media, true)
  await duration.load(media, true)
  assert.equal(calls, 2)
  finish({ duration_ms: 10000 })
  await retried
  assert.equal(labels.at(-1), '总时长 10 秒')
  await duration.load(media, true)
  assert.equal(calls, 2)
})

test('文件本身时长未知不重复读取，迟到失败也不会使新录音重试', async () => {
  let calls = 0,
    rejectOld
  const duration = createRecordingDuration({
    request: () => {
      calls++
      return calls === 1
        ? new Promise((_, reject) => (rejectOld = reject))
        : Promise.resolve({ duration_ms: null })
    },
    changed: () => {},
  })
  const old = duration.load('a'.repeat(32))
  await duration.load('b'.repeat(32))
  rejectOld(new Error('synthetic stale failure'))
  await old
  await duration.load('b'.repeat(32), true)
  assert.equal(calls, 2)
})
