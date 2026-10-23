import { callStates } from './call-history.js'
import { notificationKinds, notificationCanceledReason } from './notification-records.js'
import { diagnosticOutcomes } from './diagnostic-flow.js'

export function recordingTraceLogNotices(data) {
  const status = data?.diagnostics
  if (
    !status ||
    !['debug', 'info', 'warn', 'error'].includes(status.level) ||
    !['file_enabled', 'file_ok', 'capacity_paused'].every((k) => typeof status[k] === 'boolean')
  )
    return ['日志状态暂不可确认，缺少记录不代表失败。']
  const notices = []
  if (status.level === 'warn')
    notices.push('当前仅记录警告和错误，提示语及播放、上传成功过程可能没有记录。')
  else if (status.level === 'error')
    notices.push('当前仅记录错误，提示语及播放、上传过程可能没有记录。')
  if (!status.file_enabled) notices.push('诊断仅保留内存记录，应用重启后可能缺少此前的过程。')
  else if (status.capacity_paused)
    notices.push('日志容量已满，磁盘记录暂停；仍可查看内存中的近期记录。')
  else if (!status.file_ok) notices.push('日志文件写入异常；仍可查看内存中的近期记录。')
  return notices
}

function checked(data, id) {
  const call = data?.call
  if (
    !call ||
    call.call_id !== id ||
    ![1, 2].includes(call.slot) ||
    !['started_at', 'connected_at', 'ended_at', 'recording_at'].every(
      (k) => Number.isSafeInteger(call[k]) && call[k] >= 0,
    ) ||
    !Object.hasOwn(callStates, call.state) ||
    !['unlinked', 'available', 'missing', 'deleted'].includes(call.recording) ||
    !Array.isArray(call.notifications) ||
    call.notifications.length > 2 ||
    call.notifications.some(
      (n) =>
        !['missed', 'timeout', 'voicemail', 'voicemail_late', 'voicemail_missing'].includes(
          n.kind,
        ) ||
        !['pending', 'sent', 'canceled', 'restore_held'].includes(n.state) ||
        typeof n.error !== 'string' ||
        !['recorded_at', 'accepted_at'].every((k) => Number.isSafeInteger(n[k]) && n[k] >= 0),
    ) ||
    !Array.isArray(data.events) ||
    data.events.length > 200 ||
    data.events.some(
      (e) =>
        !Number.isSafeInteger(e.id) ||
        e.id <= 0 ||
        !Number.isSafeInteger(e.time_ms) ||
        e.time_ms <= 0 ||
        typeof e.code !== 'string',
    )
  )
    throw new Error('链路记录无法确认，请重新读取。')
  return data
}

export function recordingTraceStages(data) {
  const c = data.call,
    events = [...data.events].sort((a, b) => a.id - b.id)
  const last = (codes) => events.filter((e) => codes.includes(e.code)).at(-1)
  const greeting = last([
    'voicemail_greeting',
    'voicemail_greeting_failed',
    'voicemail_greeting_skipped',
  ])
  const played = last(['voicemail_playback_report'])
  let greetingText = greeting
    ? {
        voicemail_greeting: '板子已接受提示语命令',
        voicemail_greeting_failed:
          '提示语命令失败或结果未知：' + (diagnosticOutcomes[greeting.outcome] || '请查看运行诊断'),
        voicemail_greeting_skipped:
          '提示语未下发：' + (diagnosticOutcomes[greeting.outcome] || '原来电已变化'),
      }[greeting.code]
    : '无可用的提示语命令记录'
  if (played)
    greetingText +=
      '；' +
      ({
        683: '板子报告 AMR 播放成功',
        685: '板子报告 AMR 播放结束',
        687: '板子报告 TTS 播放成功',
        689: '板子报告 TTS 播放结束',
      }[played.event_type] || '收到播放报告')
  const upload = last(['voicemail_upload_report', 'voicemail_upload_failed'])
  return [
    {
      label: '来电',
      text:
        c.state === 'connected' && c.ended_at === 0
          ? callStates[c.state] + '；尚未收到结束上报'
          : callStates[c.state],
      at: c.ended_at || c.connected_at || c.started_at,
    },
    {
      label: '提示语',
      text: greetingText,
      at: Math.max(greeting?.time_ms || 0, played?.time_ms || 0) / 1000,
    },
    {
      label: '板子上传',
      text: upload
        ? upload.code === 'voicemail_upload_failed'
          ? '板子报告上传失败，具体原因需核对板子日志'
          : '板子报告上传成功；保存结果见下方'
        : '无可关联的上传报告，不能据此判定失败',
      at: (upload?.time_ms || 0) / 1000,
    },
    {
      label: '应用录音',
      text: {
        available: '录音已保存并关联，可读取',
        missing: '已有录音索引，文件不可读取',
        deleted: '录音已删除，去重记录保留',
        unlinked: '暂未收到关联录音；可核对下方未关联录音和运行诊断中的拒收原因',
      }[c.recording],
      at: c.recording_at,
    },
    {
      label: 'Bark',
      text: c.notifications.length
        ? c.notifications
            .map(
              (n) =>
                notificationKinds[n.kind] +
                '：' +
                {
                  sent: 'Bark 已接受',
                  pending: '等待推送或后台重试',
                  restore_held: '恢复后暂停',
                  canceled: notificationCanceledReason({ last_error: n.error }),
                }[n.state],
            )
            .join('；')
        : '尚未生成提醒，以现有等待及通知规则为准',
      at: c.notifications.at(-1)?.accepted_at || 0,
    },
  ]
}

export function createRecordingTrace({ request, changed }) {
  let stopped = false,
    revision = 0,
    state = { id: 0, busy: false, data: null, error: '' }
  const update = (values) => {
    if (!stopped) {
      state = { ...state, ...values }
      changed(state)
    }
  }
  const clear = () => {
    revision++
    update({ id: 0, busy: false, data: null, error: '' })
  }
  return {
    clear,
    dispose() {
      clear()
      stopped = true
    },
    async load(id) {
      if (stopped || !Number.isSafeInteger(id) || id <= 0 || (state.id === id && state.busy))
        return false
      const version = ++revision
      update({ id, busy: true, data: null, error: '' })
      try {
        const data = await request('/voicemails/trace/' + id, 'GET')
        if (stopped || version !== revision) return false
        update({ data: checked(data, id) })
        return true
      } catch (e) {
        if (version === revision) update({ error: e.message })
        return false
      } finally {
        if (version === revision) update({ busy: false })
      }
    },
  }
}
