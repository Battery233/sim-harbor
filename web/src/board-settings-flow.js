export function createBoardSettingsFlow({ request }) {
  let busy = false,
    uncertain = false,
    disposed = false
  return {
    async query(action) {
      if (busy || disposed) return null
      if (!['heartbeat_query', 'storage_query'].includes(action))
        throw Error('只支持已列出的查询。')
      busy = true
      try {
        const r = await request('/board/parameters', 'POST', { action })
        if (disposed) return null
        if (action === 'heartbeat_query') {
          if (!Number.isInteger(r.seconds) || r.seconds < 5 || r.seconds > 600)
            throw Error('心跳查询结果不完整。')
          uncertain = false
        } else if (
          !r.storage ||
          !['1s', '1t', '2s', '2t'].every((k) => typeof r.storage[k] === 'boolean')
        )
          throw Error('存储查询结果不完整。')
        return r
      } finally {
        busy = false
      }
    },
    async set(seconds, expected, confirmed) {
      if (busy || uncertain || disposed) return null
      if (
        !confirmed ||
        !Number.isInteger(seconds) ||
        seconds < 5 ||
        seconds > 600 ||
        !Number.isInteger(expected)
      )
        throw Error('请先查询并核对要修改的参数。')
      busy = true
      try {
        const r = await request('/board/parameters', 'POST', {
          action: 'heartbeat_set',
          seconds,
          expected,
          confirmed: true,
        })
        if (disposed) return null
        if (!r.verified || r.seconds !== seconds) throw Error('修改结果尚未确认。')
        return r
      } catch (e) {
        if (!disposed) uncertain = true
        throw e
      } finally {
        busy = false
      }
    },
    dispose() {
      disposed = true
    },
  }
}
