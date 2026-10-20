import test from 'node:test'
import assert from 'node:assert/strict'
import { createVoicemailPlayer } from './voicemail-player.js'
import { decodeVoicemail } from './voicemail-audio.js'

const call = (id) => ({ id, voicemail_id: id + 100 })
const tone = async () => ({ channelData: [Float32Array.of(0.25, -0.5)], sampleRate: 8000 })

function silenceAMR(frames) {
  const bytes = new Uint8Array(6 + frames)
  bytes.set(new TextEncoder().encode('#!AMR\n'))
  bytes.fill(0x7c, 6)
  return bytes
}

function harness(decode = tone) {
  const events = [],
    requests = [],
    waiters = [],
    contexts = [],
    decoded = []
  const player = createVoicemailPlayer({
    request: (path, method, body, options) => {
      requests.push({ path, method, body, options })
      return new Promise((resolve, reject) => waiters.push({ resolve, reject }))
    },
    async decode(bytes) {
      decoded.push(bytes)
      return decode(bytes)
    },
    createContext() {
      const ctx = {
        state: 'suspended',
        destination: {},
        resume() {
          ctx.resumed = true
          ctx.state = 'running'
          return Promise.resolve()
        },
        createBuffer(_channels, length, rate) {
          const data = new Float32Array(length)
          return {
            sampleRate: rate,
            getChannelData: () => data,
            copyToChannel(samples) {
              data.set(samples)
            },
            data,
          }
        },
        createBufferSource() {
          const source = {
            connect() {},
            disconnect() {},
            start() {
              source.started = true
            },
            stop() {
              source.stopped = true
            },
          }
          ctx.sources.push(source)
          return source
        },
        sources: [],
      }
      contexts.push(ctx)
      return ctx
    },
    onChange(state) {
      events.push(state)
    },
  })
  return { player, events, requests, waiters, contexts, decoded }
}

test('playback resumes audio, then fetches and decodes the authenticated AMR', async () => {
  const h = harness()
  const done = h.player.toggle(call(7))
  assert.equal(h.contexts[0].resumed, true)
  assert.equal(h.requests.length, 1)
  assert.deepEqual(h.requests[0], {
    path: '/calls/7/voicemail',
    method: 'GET',
    body: undefined,
    options: { types: ['audio/amr'], max: 2 * 1024 * 1024 },
  })
  const bytes = Uint8Array.of(1, 2, 3)
  h.waiters[0].resolve(new Blob([bytes]))
  await done
  assert.deepEqual(h.decoded[0], bytes)
  assert.equal(h.contexts[0].sources.length, 1)
  assert.equal(h.contexts[0].sources[0].started, true)
  assert.equal(h.contexts[0].sources[0].buffer.sampleRate, 8000)
  assert.equal(h.contexts[0].sources[0].buffer.data[0], 0.25)
  assert.deepEqual(h.events, [
    { id: 7, phase: 'loading' },
    { id: 7, phase: 'playing' },
  ])
  h.contexts[0].sources[0].onended()
  assert.deepEqual(h.events.at(-1), { id: 0, phase: 'idle' })
})

test('the same recording toggles off without a second download', async () => {
  const h = harness()
  const done = h.player.toggle(call(4))
  h.waiters[0].resolve(new Blob([Uint8Array.of(9)]))
  await done
  await h.player.toggle(call(4))
  assert.equal(h.requests.length, 1)
  assert.equal(h.contexts[0].sources[0].stopped, true)
  assert.deepEqual(h.events.at(-1), { id: 0, phase: 'idle' })
})

test('starting another recording stops the one already playing', async () => {
  const h = harness()
  const first = h.player.toggle(call(1))
  h.waiters[0].resolve(new Blob([Uint8Array.of(1)]))
  await first
  const second = h.player.toggle(call(2))
  assert.equal(h.contexts[0].sources[0].stopped, true)
  h.waiters[1].resolve(new Blob([Uint8Array.of(2)]))
  await second
  assert.equal(h.requests[1].path, '/calls/2/voicemail')
  assert.equal(h.contexts[1].sources[0].started, true)
  assert.deepEqual(h.events.at(-1), { id: 2, phase: 'playing' })
})

test('a replaced recording does not surface the abandoned download error', async () => {
  const h = harness()
  const first = h.player.toggle(call(1))
  const second = h.player.toggle(call(2))
  h.waiters[0].reject(new Error('过期'))
  h.waiters[1].resolve(new Blob([Uint8Array.of(2)]))
  await first
  await second
  assert.equal(h.contexts.at(-1).sources[0].started, true)
  assert.deepEqual(h.events.at(-1), { id: 2, phase: 'playing' })
})

test('undecodable AMR reports a playback error and does not start audio', async () => {
  const h = harness(async () => ({ channelData: [], sampleRate: 0 }))
  const done = h.player.toggle(call(3))
  h.waiters[0].resolve(new Blob([Uint8Array.of(1, 2, 3, 4, 5, 6, 7, 8, 9)]))
  await assert.rejects(done, /无法解码/)
  assert.equal(h.contexts[0].sources.length, 0)
  assert.deepEqual(h.events.at(-1), { id: 0, phase: 'idle' })
})

test('leaving the page drops a download that finishes later', async () => {
  const h = harness()
  const done = h.player.toggle(call(5))
  h.player.dispose()
  h.waiters[0].resolve(new Blob([Uint8Array.of(5)]))
  await done
  assert.equal(h.contexts[0].sources.length, 0)
  await h.player.toggle(call(6))
  assert.equal(h.requests.length, 1)
})

test('decoder errors stay on the playback control', async () => {
  const h = harness(async () => {
    throw new Error('wasm exploded')
  })
  const done = h.player.toggle(call(8))
  h.waiters[0].resolve(new Blob([Uint8Array.of(8)]))
  await assert.rejects(done, /无法解码/)
  assert.deepEqual(h.events.at(-1), { id: 0, phase: 'idle' })
})

test('AMR-NB silence frames decode to 8 kHz mono PCM', async () => {
  const frames = 4
  const audio = await decodeVoicemail(silenceAMR(frames))
  assert.equal(audio.sampleRate, 8000)
  assert.equal(audio.channelData.length, 1)
  assert.equal(audio.channelData[0].length, frames * 160)
  const junk = await decodeVoicemail(Uint8Array.of(1, 2, 3, 4, 5, 6, 7, 8, 9))
  assert.equal(junk.channelData.length, 0)
})
