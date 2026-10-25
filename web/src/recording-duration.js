export function recordingDurationLabel(ms) {
  if (!Number.isSafeInteger(ms) || ms <= 0) return '总时长未知'
  return '总时长 ' + Number((ms / 1000).toFixed(2)) + ' 秒'
}

// Successful reads are reused. Only an explicit refresh retries failed reads;
// changing recordings or leaving retires pending results.
export function createRecordingDuration({ request, changed }) {
  let revision = 0,
    stopped = false,
    currentMedia,
    failed = false
  return {
    async load(media, retry = false) {
      if (stopped || (media === currentMedia && (!retry || !failed))) return
      currentMedia = media
      failed = false
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
        if (label === '总时长未知' && data?.duration_ms !== null)
          throw new Error('invalid duration response')
      } catch {
        // A missing or unreadable recording must not break the call list.
        if (version === revision) failed = true
      }
      if (!stopped && version === revision) {
        changed(label)
      }
    },
    dispose() {
      stopped = true
      revision++
    },
  }
}
