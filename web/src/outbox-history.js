import { cardFilterOptions, cardLocationsFresh } from './inbox-cards.js'

export function outgoingFilterOptions(state, now) {
  return cardFilterOptions(state, now).map((option) => ({
    ...option,
    label:
      option.value === ''
        ? '全部发件记录'
        : option.value === 'unknown'
          ? '未关联卡记录（历史或缺少记录）'
          : option.label,
  }))
}

export function outgoingCardLabel(record) {
  const name = record?.card_current_name || '未命名卡'
  if (!record?.card_id)
    return '未关联卡记录' + (record?.card_tail ? ' · SIM编号尾号 ' + record.card_tail : '')
  return name + (record.card_number ? ' · ' + record.card_number : ' · 未记录号码')
}

export function sendSlotOptions(state, now) {
  const fresh = cardLocationsFresh(state, now)
  return [1, 2].map((slot) => {
    const card = fresh ? state?.cards?.find((c) => c.current_slot === slot) : null
    return {
      value: String(slot),
      label: `卡槽 ${slot} · ${card ? (card.name || '未命名卡') + (card.number ? ' · ' + card.number : '') : '待核对'}`,
    }
  })
}

// History filtering never touches the separate pending send/confirmation flow.
// Filter changes retire old replies and reset the paging cursor before any GET.
export function createOutboxHistory({ request, changed = () => {} }) {
  let revision = 0,
    stopped = false,
    filter = ''
  let state = { items: [], next: 0 }
  return {
    async load(card = '', more = false) {
      if (stopped) return false
      if (card && card !== 'unknown' && !/^[a-f0-9]{16}$/.test(card))
        throw new Error('发件卡筛选无效。')
      const reset = filter !== card
      if (reset) {
        filter = card
        state = { items: [], next: 0 }
        changed({ ...state })
      }
      const version = ++revision
      const before = more && !reset ? state.next : 0
      const query = new URLSearchParams()
      if (card) query.set('card', card)
      if (before) query.set('before', before)
      try {
        const data = await request('/outgoing' + (query.size ? '?' + query : ''), 'GET')
        if (stopped || version !== revision) return false
        if (
          !Array.isArray(data?.items) ||
          data.items.length > 50 ||
          !Number.isSafeInteger(data.next_before) ||
          data.next_before < 0 ||
          data.items.some(
            (o) =>
              !o ||
              typeof o.id !== 'string' ||
              !Number.isSafeInteger(o.sequence) ||
              o.sequence <= 0 ||
              (card === 'unknown' ? !!o.card_id : !!card && o.card_id !== card),
          )
        )
          throw new Error('发件记录响应不完整或卡归属不匹配，请重新读取。')
        const merged = new Map(state.items.map((o) => [o.id, o]))
        data.items.forEach((o) => merged.set(o.id, o))
        const items = [...merged.values()].sort((a, b) => b.sequence - a.sequence)
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
    dispose() {
      stopped = true
      revision++
    },
  }
}
