// Common PLMNs from the locally downloaded vendor v150 stat reference.
const networkNames = {
  46000: '中国移动',
  46002: '中国移动',
  46007: '中国移动',
  46001: '中国联通',
  46006: '中国联通',
  46009: '中国联通',
  46003: '中国电信',
  46005: '中国电信',
  46011: '中国电信',
}

function text(value) {
  return typeof value === 'string' ? value.trim() : ''
}

// The board percent-encodes SSIDs. Decode only for display, once, without
// treating a literal '+' as a space or changing the reported status.
export function wifiName(value) {
  if (typeof value !== 'string') return ''
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

export function slotView(state, slot, now = Date.now() / 1000) {
  const at = Number(state?.status_received_at || 0)
  const currentSession = state?.status_current_session !== false
  const fresh =
    !!state?.online &&
    currentSession &&
    now >= at &&
    now - at <= (state.stale_seconds || 345) &&
    at > 0
  const info = state?.reported?.slotInfo || {}
  const raw = info[`slot${slot}_sta`]
  const names = { OK: '正常', NOSIM: '未插卡', POWON: '启动中', ERR: '异常' }
  const signal = Number(info[`sim${slot}_dbm`])
  const previous = names[raw] || (raw ? `状态 ${raw}` : '未知')
  const plmn = text(info[`sim${slot}_plmn`])
  const imsi = text(info[`sim${slot}_imsi`])
  return {
    name: text(info[`sim${slot}_scName`]) || `卡槽 ${slot}`,
    number: raw === 'OK' ? text(info[`sim${slot}_msIsdn`]) : '',
    label: !at
      ? '尚未查询'
      : !state?.online
        ? '离线 · 上次' + previous
        : !currentSession
          ? '正在核对'
          : !fresh
            ? '待刷新 · 上次' + previous
            : previous,
    good: fresh && raw === 'OK',
    signal:
      at > 0 &&
      info[`sim${slot}_dbm`] != null &&
      info[`sim${slot}_dbm`] !== '' &&
      Number.isFinite(signal) &&
      signal >= 0 &&
      signal <= 99
        ? `${signal}%`
        : '—',
    operator: Object.hasOwn(networkNames, plmn) ? networkNames[plmn] : plmn || '—',
    operatorCode: plmn,
    crossBorderRoaming:
      fresh &&
      raw === 'OK' &&
      /^\d{5,6}$/.test(plmn) &&
      /^\d{14,15}$/.test(imsi) &&
      !plmn.startsWith('000') &&
      !imsi.startsWith('000') &&
      plmn.slice(0, 3) !== imsi.slice(0, 3),
    fresh,
  }
}

export function mergeMessages(current, incoming) {
  const map = new Map(current.map((m) => [m.id, m]))
  incoming.forEach((m) => map.set(m.id, m))
  return [...map.values()].sort((a, b) => b.id - a.id)
}

// Formatting comes from the explicit server policy; never unescape arbitrary
// SMS text in the browser, or change the immutable raw body.
export function messageText(message, raw = false) {
  if (!raw && typeof message?.display_body === 'string') return message.display_body
  return typeof message?.body === 'string' ? message.body : ''
}

export function hasFormattedText(message) {
  return (
    typeof message?.display_body === 'string' && message.display_body !== messageText(message, true)
  )
}
