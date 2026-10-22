export const callStates = {
  ringing: '振铃中',
  missed: '未接来电',
  connected: '已接通（设备报告）',
  answered: '已接通并结束',
  unknown: '状态待核对',
}

export function callerLabel(phone) {
  if (typeof phone !== 'string' || !phone.trim()) return '号码未提供'
  return phone.trim().toLowerCase() === 'anonymous' ? '匿名来电' : phone.trim()
}

export function ringingLabel(call) {
  if (call?.state === 'ringing') return '尚未结束'
  if (
    call?.state === 'unknown' ||
    !Number.isSafeInteger(call?.ring_seconds) ||
    call.ring_seconds < 0
  )
    return '时长未确认'
  return call.ring_seconds + ' 秒'
}

export function callPushLabel(call) {
  if (call?.notification_state === 'canceled' && call?.notification_error === 'voicemail_deleted')
    return '录音已删除，留言提醒已取消'
  const fallback =
    call?.notification_kind === 'timeout'
      ? ' · 兜底提醒'
      : call?.notification_kind === 'voicemail'
        ? ' · 留言'
        : call?.notification_kind === 'voicemail_missing'
          ? ' · 录音未收到'
          : call?.notification_kind === 'voicemail_late'
            ? ' · 迟到录音已收到'
            : ''
  return (
    ({
      sent: '推送成功',
      pending: call?.notification_failures ? '等待重试' : '等待推送',
      restore_held: '恢复后待核对',
      canceled: '提醒已取消 · 来电待核对',
      not_queued: '未生成提醒',
    }[call?.notification_state] || '未生成提醒') +
    fallback +
    (call?.voicemail_deleted ? ' · 录音已删除' : '')
  )
}

function checkedCall(call, id) {
  if (
    !call ||
    !Number.isSafeInteger(call.id) ||
    call.id <= 0 ||
    (id && call.id !== id) ||
    ![1, 2].includes(call.slot) ||
    typeof call.phone !== 'string' ||
    !Object.hasOwn(callStates, call.state) ||
    (call.voicemail_deleted != null && typeof call.voicemail_deleted !== 'boolean') ||
    (call.voicemail_id != null &&
      (!Number.isSafeInteger(call.voicemail_id) || call.voicemail_id <= 0)) ||
    (call.voicemail_bytes != null &&
      (!Number.isSafeInteger(call.voicemail_bytes) || call.voicemail_bytes <= 0))
  )
    throw new Error('来电记录内容不完整，请重新读取。')
  return call
}

// Entirely read-only. A changed card/search retires both old data and cursors.
export function createCallHistory({ request, changed = () => {}, grouped = false }) {
  let revision = 0,
    stopped = false,
    activeFilter = ''
  let state = { items: [], next: 0 }
  return {
    async load({ card = '', q = '', group = 0 } = {}, more = false) {
      if (stopped) return false
      q = q.trim()
      if (q.length > 128 || (card && card !== 'unknown' && !/^[a-f0-9]{16}$/.test(card)))
        throw new Error('来电筛选条件无效。')
      if (!Number.isSafeInteger(group) || group < 0 || (group && (card || q || grouped)))
        throw new Error('来电筛选条件无效。')
      const key = JSON.stringify([card, q, group])
      const reset = key !== activeFilter
      if (reset) {
        activeFilter = key
        state = { items: [], next: 0 }
        changed({ ...state })
      }
      const version = ++revision
      const query = new URLSearchParams()
      if (card) query.set('card', card)
      if (q) query.set('q', q)
      if (group) query.set('group', group)
      if (more && !reset && state.next) query.set('before', state.next)
      try {
        const data = await request(
          (grouped ? '/call-groups' : '/calls') + (query.size ? '?' + query : ''),
          'GET',
        )
        if (stopped || version !== revision) return false
        if (
          !Array.isArray(data?.items) ||
          data.items.length > 50 ||
          !Number.isSafeInteger(data.next_before) ||
          data.next_before < 0
        )
          throw new Error('来电列表响应不完整，请重新读取。')
        const merged = new Map(state.items.map((call) => [call.id, call]))
        for (const call of data.items) {
          const record = grouped ? checkedGroup(call).latest : checkedCall(call)
          if (card === 'unknown' ? !!record.card_id : !!card && record.card_id !== card)
            throw new Error('来电卡归属不匹配，请重新读取。')
          merged.set(call.id, call)
        }
        const items = [...merged.values()].sort((a, b) =>
          grouped ? b.latest.id - a.latest.id : b.id - a.id,
        )
        state = {
          items,
          next: more || reset || items.length <= 50 ? data.next_before : state.next,
        }
        changed({ ...state })
        return true
      } catch (e) {
        if (stopped || version !== revision) return false
        throw e
      }
    },
    async detail(id) {
      if (!Number.isSafeInteger(id) || id <= 0) throw new Error('来电记录编号无效。')
      const result = await request('/calls/' + id, 'GET')
      return stopped ? null : checkedCall(result, id)
    },
    async groupDetail(id) {
      if (!Number.isSafeInteger(id) || id <= 0) throw new Error('来电记录编号无效。')
      const result = await request('/call-groups/' + id, 'GET')
      return stopped ? null : checkedGroup(result)
    },
    dispose() {
      stopped = true
      revision++
    },
  }
}

function checkedGroup(group) {
  if (
    !group ||
    !Number.isSafeInteger(group.id) ||
    group.id <= 0 ||
    !Number.isSafeInteger(group.count) ||
    group.count < 1 ||
    !Number.isSafeInteger(group.missed_count) ||
    group.missed_count < 0 ||
    group.missed_count > group.count
  )
    throw new Error('来电汇总内容不完整，请重新读取。')
  checkedCall(group.latest)
  return group
}
export function createCallGroups(options) {
  return createCallHistory({ ...options, grouped: true })
}

export function groupContainsCall(group, call) {
  if (!group || !call) return false
  if (group.id === call.id && group.latest.id === call.id) return true
  return (
    !!call.card_id &&
    !!call.phone.trim() &&
    call.phone.toLowerCase() !== 'anonymous' &&
    group.latest.card_id === call.card_id &&
    group.latest.phone === call.phone &&
    group.id <= call.id &&
    group.latest.id >= call.id
  )
}
