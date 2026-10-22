// Listening is a browser-local convenience, never a server delivery/read receipt.
// Random media IDs are independent of the secret upload credential.
const key = 'simharbor.voicemail-played.v1'
const validID = (id) => typeof id === 'string' && /^[a-f0-9]{32}$/.test(id)
export function createListeningHistory(storage) {
  let played = new Set()
  try {
    const ids = JSON.parse(storage?.getItem(key) || '[]')
    if (Array.isArray(ids)) played = new Set(ids.filter(validID).slice(-10000))
  } catch {
    /* Disabled storage or a damaged local preference. */
  }
  return {
    has: (id) => played.has(id),
    mark(id) {
      if (!validID(id)) return false
      played.delete(id)
      played.add(id)
      if (played.size > 10000) played.delete(played.values().next().value)
      try {
        storage?.setItem(key, JSON.stringify([...played]))
      } catch {
        /* Session-only fallback. */
      }
      return true
    },
  }
}
export function recordingBadge(ids, history) {
  if (!Array.isArray(ids) || !ids.length) return ''
  return ids.some((id) => !history.has(id)) ? '📼 未播放' : '📼 已播放'
}
