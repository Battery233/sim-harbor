// Playback markers are persisted by the server after audio actually starts.
export function recordingBadge(total, unplayed) {
  if (!Number.isInteger(total) || total <= 0) return ''
  return unplayed > 0 ? '📼 未播放' : '📼 已播放'
}
export async function markRecordingPlayed(request, media) {
  if (typeof media !== 'string' || !/^[a-f0-9]{32}$/.test(media)) throw new Error('录音编号无效。')
  const result = await request('/voicemails/' + media + '/played', 'POST', {})
  if (!Number.isSafeInteger(result?.played_at) || result.played_at <= 0)
    throw new Error('播放状态未能确认，请刷新核对。')
  globalThis.window?.dispatchEvent(new Event('simharbor:recording-played'))
  return result.played_at
}
