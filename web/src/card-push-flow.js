export function createCardPushFlow({ request, onChanged = () => {} }) {
  let busy = false,
    uncertain = false,
    disposed = false
  return {
    async set(card, enabled) {
      if (busy || uncertain || disposed) return null
      if (
        !/^[a-f0-9]{16}$/.test(card?.id) ||
        typeof card.notification_enabled !== 'boolean' ||
        typeof enabled !== 'boolean'
      )
        throw Error('无法确认这张卡，请刷新状态。')
      busy = true
      try {
        const result = await request(`/cards/${card.id}/notifications`, 'PUT', {
          enabled,
          expected: card.notification_enabled,
        })
        if (disposed) return null
        if (result.card_id !== card.id || result.notification_enabled !== enabled)
          throw Error('开关结果未确认。')
        onChanged(result)
        return result
      } catch (e) {
        if (!disposed) uncertain = true
        throw e
      } finally {
        busy = false
      }
    },
    async reload() {
      if (busy || disposed) return null
      busy = true
      try {
        const state = await request('/state')
        if (!disposed) {
          uncertain = false
          onChanged(state)
          return state
        }
        return null
      } finally {
        busy = false
      }
    },
    dispose() {
      disposed = true
    },
  }
}
