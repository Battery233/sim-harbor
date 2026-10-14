export function createBoardHistoryFlow({ request }) {
  let generation = 0,
    disposed = false,
    busy = false
  return {
    async load(kind, slot, offset) {
      if (disposed || busy) return null
      if (
        !['sms', 'calls'].includes(kind) ||
        ![1, 2].includes(slot) ||
        !Number.isInteger(offset) ||
        offset < 1 ||
        offset > 1000
      )
        throw Error('查询范围不正确。')
      const version = generation
      busy = true
      try {
        const r = await request('/board/history', 'POST', { kind, slot, offset })
        if (disposed || version !== generation) return null
        if (
          r.kind !== kind ||
          r.slot !== slot ||
          r.offset !== offset ||
          !Array.isArray(r.items) ||
          r.items.length > 10 ||
          typeof r.has_more !== 'boolean' ||
          r.items.some((x) => x.slot !== slot || ![0, 1].includes(x.direction))
        )
          throw Error('查询结果范围无法确认。')
        return r
      } finally {
        busy = false
      }
    },
    invalidate() {
      generation++
    },
    dispose() {
      disposed = true
      generation++
    },
  }
}
export function boardStatusView(value, showIdentity = false) {
  if (Array.isArray(value)) return value.map((x) => boardStatusView(x, showIdentity))
  if (!value || typeof value !== 'object') return value
  return Object.fromEntries(
    Object.entries(value).map(([key, v]) => [
      key,
      /password|token|secret|key$|iv$/i.test(key)
        ? '已隐藏'
        : !showIdentity && /iccid|imsi/i.test(key)
          ? '身份信息已隐藏'
          : boardStatusView(v, showIdentity),
    ]),
  )
}
