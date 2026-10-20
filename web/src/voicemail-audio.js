// AMR-NB/WB to PCM. The decoder is loaded on the first play so the rest of the UI
// does not download the WASM until someone presses 播放留言.
export async function decodeVoicemail(bytes) {
  const { default: decode } = await import('@audio/decode-amr')
  return decode(bytes)
}

export function createVoicemailContext() {
  let context
  return () => {
    const Ctx = globalThis.AudioContext || globalThis.webkitAudioContext
    if (typeof Ctx !== 'function') throw new Error('这个浏览器不能播放留言。')
    if (!context || context.state === 'closed') context = new Ctx()
    return context
  }
}
