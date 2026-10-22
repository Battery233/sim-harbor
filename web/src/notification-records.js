export const notificationKinds = {
  sms: '短信',
  missed: '未接来电',
  timeout: '来电结束未确认',
  voicemail: '有留言',
  voicemail_missing: '录音未收到',
  voicemail_late: '迟到录音已收到',
}
export function notificationRecordLink(row) {
  return row.source === 'call' ? '?call=' + row.call_id : '?message=' + row.message_id
}
export function notificationCanceledReason(row) {
  return (
    {
      voicemail_deleted: '录音已删除，不再推送这条留言提醒',
      voicemail_arrived: '录音已收到，原缺失提醒停止重试',
      voicemail_wait_canceled: '录音等待提醒已停止，或来电状态有变化',
      card_notifications_disabled: '这张卡的通知已关闭',
      call_state_uncertain: '来电状态有变化，不再发送这条提醒',
    }[row.last_error] || (row.message_deleted ? '原短信已删除，不再推送' : '这条提醒已取消')
  )
}
function checked(row) {
  if (
    !row ||
    !['sms', 'call'].includes(row.source) ||
    !Number.isSafeInteger(row.id) ||
    row.id <= 0 ||
    row.key !== row.source + ':' + row.id ||
    ![1, 2].includes(row.slot) ||
    !['sent', 'pending', 'canceled', 'restore_held'].includes(row.state) ||
    !Object.hasOwn(notificationKinds, row.kind) ||
    (row.source === 'sms' ? row.kind !== 'sms' : row.kind === 'sms') ||
    !Number.isSafeInteger(row.source === 'call' ? row.call_id : row.message_id) ||
    (row.source === 'call' ? row.call_id : row.message_id) <= 0 ||
    !['failures', 'next_at', 'recorded_at', 'accepted_at'].every(
      (k) => Number.isSafeInteger(row[k]) && row[k] >= 0,
    )
  )
    throw new Error('推送记录内容不完整，请刷新核对。')
  return row
}
export function createNotificationRecords({ request, changed = () => {} }) {
  let stopped = false,
    generation = 0,
    filter = '',
    view = { items: [], next_before: '', summary: null }
  return {
    async load({ state = 'all', source = 'all' } = {}, more = false) {
      if (stopped) return false
      if (
        !['all', 'sms', 'call'].includes(source) ||
        !['all', 'pending', 'sent', 'restore_held', 'canceled'].includes(state)
      )
        throw new Error('推送筛选条件无效。')
      const key = state + ':' + source
      if (filter !== key) {
        filter = key
        view = { items: [], next_before: '', summary: null }
        changed(view)
        more = false
      }
      if (more && !view.next_before) return false
      const version = ++generation
      const query = new URLSearchParams({ state, type: source })
      if (more) query.set('before', view.next_before)
      let data
      try {
        data = await request('/notification-records?' + query, 'GET')
      } catch (error) {
        if (stopped || generation !== version) return false
        throw error
      }
      if (stopped || generation !== version) return false
      if (
        !Array.isArray(data?.items) ||
        data.items.length > 50 ||
        typeof data.next_before !== 'string' ||
        data.next_before.length > 192 ||
        !data.summary
      )
        throw new Error('推送列表响应不完整，请刷新。')
      const rows = data.items.map(checked)
      if (
        rows.some(
          (row) =>
            (source !== 'all' && row.source !== source) || (state !== 'all' && row.state !== state),
        )
      )
        throw new Error('推送记录与筛选条件不符，请刷新。')
      const items = more
        ? [...new Map([...view.items, ...rows].map((row) => [row.key, row])).values()]
        : rows
      view = { ...data, items }
      changed(view)
      return true
    },
    dispose() {
      stopped = true
      generation++
    },
  }
}
