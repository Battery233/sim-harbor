export function recordingDurationLabel(ms) {
  if (!Number.isSafeInteger(ms) || ms <= 0) return '总时长未知'
  return '总时长 ' + Number((ms / 1000).toFixed(2)) + ' 秒'
}

// Each displayed recording is read once. Polling the surrounding list does not
// repeat the request; changing recordings or leaving retires pending results.
export function createRecordingDuration({ request, changed }) {
  let revision = 0,
    stopped = false,
    currentMedia
  return {
    async load(media) {
      if (stopped || media === currentMedia) return
      currentMedia = media
      const version = ++revision
      if (typeof media !== 'string' || !/^[a-f0-9]{32}$/.test(media)) {
        changed('总时长未知')
        return
      }
      changed('正在读取时长…')
      let label = '总时长未知'
      try {
        const data = await request('/voicemails/duration/' + media, 'GET')
        label = recordingDurationLabel(data?.duration_ms)
      } catch {
        // A missing or unreadable recording must not break the call list.
      }
      if (!stopped && version === revision) changed(label)
    },
    dispose() {
      stopped = true
      revision++
    },
  }
}
