export function createInboxDeletion({ state, api, session, onDeleted }) {
  let revision = 0
  function reset() {
    revision++
    state.target = null
    state.busy = false
    state.error = ''
  }
  function request(message) {
    if (state.busy || !Number.isSafeInteger(message?.id) || message.id < 1) return false
    revision++
    state.target = { ...message }
    state.error = ''
    return true
  }
  function cancel() {
    if (state.busy) return false
    reset()
    return true
  }
  async function confirm() {
    if (!state.target || state.busy) return false
    const id = state.target.id,
      attempt = revision,
      identity = session()
    state.busy = true
    state.error = ''
    try {
      const result = await api(`/messages/${id}/delete`, 'POST', { confirmed: true })
      if (revision !== attempt || session() !== identity) return false
      if (result.ok !== true || result.id !== id)
        throw new Error('未能确认删除结果，请刷新查看；不会自动重试。')
      reset()
      await onDeleted(id)
      return true
    } catch (e) {
      if (revision === attempt && session() === identity) state.error = e.message
      return false
    } finally {
      if (revision === attempt && session() === identity) state.busy = false
    }
  }
  return { request, cancel, confirm, reset }
}
