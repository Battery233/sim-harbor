import test from 'node:test'
import assert from 'node:assert/strict'
import {
  createRecordingTrace,
  recordingTraceStages,
  recordingTraceLogNotices,
} from './recording-trace.js'

const data = (id = 1) => ({
  call: {
    call_id: id,
    slot: 1,
    started_at: 1000,
    connected_at: 1002,
    ended_at: 1010,
    recording_at: 0,
    recording: 'unlinked',
    state: 'answered',
    notifications: [],
  },
  events: [],
})
test('仅在日志受限时说明证据范围，不把缺少日志判成录音失败', () => {
  const status = { level: 'info', file_enabled: true, file_ok: true, capacity_paused: false }
  const notices = (change) => recordingTraceLogNotices({ diagnostics: { ...status, ...change } })
  assert.deepEqual(notices({}), [])
  assert.deepEqual(notices({ level: 'debug' }), [])
  assert.match(notices({ level: 'warn' })[0], /仅记录警告和错误/)
  assert.match(notices({ level: 'error' })[0], /仅记录错误/)
  assert.match(notices({ file_enabled: false, file_ok: false })[0], /仅保留内存记录/)
  assert.match(notices({ capacity_paused: true, file_ok: false })[0], /磁盘记录暂停/)
  assert.match(notices({ file_ok: false })[0], /文件写入异常/)
  assert.match(recordingTraceLogNotices({})[0], /状态暂不可确认/)
  assert.match(notices({ level: 'unknown' })[0], /状态暂不可确认/)
  assert.match(notices({ file_enabled: 'false' })[0], /状态暂不可确认/)
})
test('缺少诊断不当失败，板子上传报告与实际保存分开表达', () => {
  let stages = recordingTraceStages(data())
  assert.match(stages[2].text, /不能据此判定失败/)
  assert.match(stages[3].text, /暂未收到关联/)
  const d = data()
  d.call.recording = 'available'
  d.events = [
    { id: 1, time_ms: 1011000, code: 'voicemail_upload_failed' },
    { id: 2, time_ms: 1012000, code: 'voicemail_greeting' },
  ]
  stages = recordingTraceStages(d)
  assert.match(stages[1].text, /接受提示语命令/)
  assert.match(stages[2].text, /报告上传失败/)
  assert.match(stages[3].text, /已保存并关联/)
})
test('链路只读，关闭、换来电、离开页面后旧响应不覆盖新记录', async () => {
  const pending = [],
    calls = []
  let state
  const flow = createRecordingTrace({
    request: (...args) => {
      calls.push(args)
      return new Promise((resolve) => pending.push(resolve))
    },
    changed: (s) => {
      state = s
    },
  })
  const old = flow.load(1)
  assert.equal(await flow.load(1), false)
  const recent = flow.load(2)
  pending[1](data(2))
  await recent
  pending[0](data(1))
  await old
  assert.equal(state.data.call.call_id, 2)
  const closing = flow.load(2)
  flow.clear()
  pending[2](data(2))
  await closing
  assert.equal(state.data, null)
  const leaving = flow.load(1)
  flow.dispose()
  pending[3](data(1))
  await leaving
  assert.equal(state.data, null)
  assert.ok(calls.every(([, method]) => method === 'GET'))
})
test('错误来电编号和结构拒绝显示，不重试请求', async () => {
  let state,
    calls = 0
  const flow = createRecordingTrace({
    request: async () => {
      calls++
      return data(2)
    },
    changed: (s) => {
      state = s
    },
  })
  assert.equal(await flow.load(1), false)
  assert.equal(state.data, null)
  assert.match(state.error, /无法确认/)
  assert.equal(calls, 1)
})
