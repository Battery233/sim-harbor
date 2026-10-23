// Plays one voicemail at a time. The AMR bytes come from the authenticated
// download; decoding and Web Audio are injected so tests do not need a browser.
export function createVoicemailPlayer({
  request,
  decode,
  createContext,
  onChange,
  resourceFor = (call) => (call?.voicemail_id ? '/calls/' + call.id + '/voicemail' : null),
}) {
  let generation = 0,
    current = null,
    disposed = false
  function stale(gen) {
    return disposed || gen !== generation
  }
  function publish(id, phase) {
    onChange?.({ id, phase })
  }
  function silence(source) {
    if (!source) return
    source.onended = null
    try {
      source.stop()
    } catch {
      /* Already stopped or never started. */
    }
    try {
      source.disconnect()
    } catch {
      /* Nothing was connected. */
    }
  }
  function stop() {
    generation += 1
    const prev = current
    current = null
    silence(prev?.source)
    if (prev) publish(0, 'idle')
  }
  function finish(source) {
    if (current?.source !== source) return
    silence(source)
    current = null
    publish(0, 'idle')
  }
  function pcm(audio) {
    const samples = audio?.channelData?.[0]
    const rate = audio?.sampleRate
    if (!(samples instanceof Float32Array) || samples.length === 0 || !(rate > 0))
      throw new Error('这段留言无法解码。')
    return { samples, rate }
  }
  async function toggle(call) {
    if (disposed || !call?.id) return
    const path = resourceFor(call)
    if (!path) return
    if (current?.id === call.id) {
      stop()
      return
    }
    stop()
    const gen = generation
    current = { id: call.id, phase: 'loading' }
    publish(call.id, 'loading')
    let source
    try {
      const context = createContext()
      // Resume inside the click gesture, but handle rejection immediately even
      // if the download fails or this playback is retired before it completes.
      const unlocking = Promise.resolve(context.resume?.()).then(
        () => true,
        () => false,
      )
      const blob = await request(path, 'GET', undefined, {
        types: ['audio/amr'],
        max: 2 * 1024 * 1024,
      })
      if (stale(gen)) return
      const bytes = new Uint8Array(await blob.arrayBuffer())
      if (stale(gen)) return
      let audio
      try {
        audio = await decode(bytes)
      } catch {
        throw new Error('这段留言无法解码。')
      }
      if (stale(gen)) return
      if (!(await unlocking)) throw new Error('无法启动声音，请重新点击播放。')
      if (stale(gen)) return
      const { samples, rate } = pcm(audio)
      const buffer = context.createBuffer(1, samples.length, rate)
      if (typeof buffer.copyToChannel === 'function') buffer.copyToChannel(samples, 0)
      else buffer.getChannelData(0).set(samples)
      source = context.createBufferSource()
      source.buffer = buffer
      source.connect(context.destination)
      source.onended = () => finish(source)
      source.start(0)
      if (stale(gen)) {
        silence(source)
        return
      }
      current = { id: call.id, phase: 'playing', source }
      publish(call.id, 'playing')
    } catch (error) {
      silence(source)
      if (stale(gen)) return
      current = null
      publish(0, 'idle')
      throw error
    }
  }
  return {
    toggle,
    stop,
    dispose() {
      if (disposed) return
      disposed = true
      stop()
      createContext.dispose?.()
    },
  }
}
