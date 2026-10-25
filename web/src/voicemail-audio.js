// AMR-NB to PCM. The decoder is loaded on the first play so the rest of the UI
// does not download the WASM until someone presses 播放留言.
export async function decodeVoicemail(bytes) {
  const data = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes)
  // NO_DATA needs only one stored byte, yet expands to 160 float32 samples.
  // Bound frames before invoking WASM, which holds frame copies plus a merged
  // PCM array; Web Audio then creates another copy. Ten minutes is 18.3 MiB
  // per PCM array. This limits playback only, never saving or downloading.
  const sizes = [12, 13, 15, 17, 19, 20, 26, 31, 5, -1, -1, -1, -1, -1, -1, 0]
  if (
    data.length > 2 * 1024 * 1024 ||
    data.length <= 6 ||
    ![35, 33, 65, 77, 82, 10].every((value, i) => data[i] === value)
  )
    throw new Error('这段留言无法解码。')
  let frames = 0
  for (let offset = 6; offset < data.length;) {
    const size = sizes[(data[offset] >> 3) & 15]
    if (size < 0 || offset + 1 + size > data.length) throw new Error('这段留言无法解码。')
    if (++frames > 10 * 60 * 50) {
      const error = new Error('录音超过网页播放的 10 分钟上限，请下载原始 AMR 播放。')
      error.code = 'voicemail_playback_limit'
      throw error
    }
    offset += 1 + size
  }
  const { default: decode } = await import('@audio/decode-amr')
  return decode(data)
}

export function createVoicemailContext() {
  let context
  const acquire = () => {
    const Ctx = globalThis.AudioContext || globalThis.webkitAudioContext
    if (typeof Ctx !== 'function') throw new Error('这个浏览器不能播放留言。')
    if (!context || context.state === 'closed') context = new Ctx()
    return context
  }
  acquire.dispose = () => {
    const previous = context
    context = undefined
    if (!previous || previous.state === 'closed') return
    try {
      Promise.resolve(previous.close()).catch(() => {})
    } catch {
      /* Cleanup must not interrupt leaving the page. */
    }
  }
  return acquire
}
