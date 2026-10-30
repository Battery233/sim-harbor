// AMR-NB to PCM. The decoder is loaded on the first play so the rest of the UI
// does not download the WASM until someone presses 播放留言.
export async function decodeVoicemail(bytes) {
  const data = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes)
  // NO_DATA needs only one stored byte, yet expands to 160 float32 samples.
  // Bound frames before invoking WASM, which holds frame copies plus a merged
  // PCM array; Web Audio then creates another copy. Ten minutes is 18.3 MiB
  // per PCM array. This limits playback only, never saving or downloading.
  // Include the frame header and match @audio/decode-amr's NB frame walk,
  // including its handling of reserved types. Otherwise a tolerated damaged
  // header can make our memory estimate disagree with the actual decoder.
  const sizes = [13, 14, 16, 18, 20, 21, 27, 32, 6, 6, 6, 6, 1, 1, 1, 1]
  if (
    data.length > 2 * 1024 * 1024 ||
    data.length <= 6 ||
    ![35, 33, 65, 77, 82, 10].every((value, i) => data[i] === value)
  )
    throw new Error('这段留言无法解码。')
  let frames = 0,
    supported = 0,
    damaged = false
  for (let offset = 6; offset < data.length;) {
    const type = (data[offset] >> 3) & 15
    const size = sizes[type]
    if (offset + size > data.length) {
      damaged = true
      break // The decoder also stops before an incomplete final frame.
    }
    if (type <= 8 || type === 15) supported++
    else damaged = true
    if (data[offset] & 0x83) damaged = true
    if (++frames > 10 * 60 * 50) {
      const error = new Error('录音超过网页播放的 10 分钟上限，请下载原始 AMR 播放。')
      error.code = 'voicemail_playback_limit'
      throw error
    }
    offset += size
  }
  if (!supported) throw new Error('这段留言无法解码。')
  const { default: decode } = await import('@audio/decode-amr')
  const audio = await decode(data)
  if (
    audio.sampleRate !== 8000 ||
    audio.channelData.length !== 1 ||
    !audio.channelData[0]?.length ||
    audio.channelData[0].length > frames * 160
  )
    throw new Error('这段留言无法解码。')
  if (damaged)
    audio.warning = '录音数据有异常，已尝试容错播放，可能有杂音或缺失；下载保留原始 AMR。'
  return audio
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
