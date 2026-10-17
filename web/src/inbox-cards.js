export function cardLocationsFresh(state, now = Date.now() / 1000) {
  return (
    !!state?.online &&
    state?.status_current_session === true &&
    state?.card_locations_verified === true &&
    state.status_received_at > 0 &&
    now >= state.status_received_at &&
    now - state.status_received_at <= state.stale_seconds
  )
}
export function cardLocation(state, card, now = Date.now() / 1000) {
  return cardLocationsFresh(state, now)
    ? card.current_slot
      ? '当前卡槽 ' + card.current_slot
      : '历史卡'
    : '位置待确认'
}
export function cardNumber(card) {
  return typeof card?.number === 'string' ? card.number.trim() : ''
}
export function cardDisambiguator(cards, card) {
  return cards.filter(
    (c) => c.name === card.name && (cardNumber(c) || c.tail) === (cardNumber(card) || card.tail),
  ).length > 1 || !card.name
    ? card.id.slice(-6)
    : ''
}
export function cardFilterOptions(state, now = Date.now() / 1000) {
  const fresh = cardLocationsFresh(state, now)
  const cards = [...(state?.cards || [])].sort((a, b) =>
    fresh ? (a.current_slot || 3) - (b.current_slot || 3) : 0,
  )
  return [
    { value: '', label: '全部短信' },
    ...cards.map((card) => ({
      value: card.id,
      label: `${cardLocation(state, card, now)} · ${card.name || '未命名卡'}${cardNumber(card) ? ' · ' + cardNumber(card) : card.tail ? ' · SIM编号尾号 ' + card.tail : ' · 未记录号码'}${cardDisambiguator(cards, card) ? ' · ' + cardDisambiguator(cards, card) : ''}`,
    })),
    { value: 'unknown', label: '来源未确认（历史或缺少身份）' },
  ]
}
export function messageSource(message, cards = []) {
  if (message.card_name) return message.card_name
  if (!message.card_id) return '来源未确认'
  const saved = cards?.find((card) => card.id === message.card_id)
  const name = typeof saved?.name === 'string' ? saved.name.trim() : ''
  return name ? `${name} · 自动识别` : '未记录收件时卡名'
}
