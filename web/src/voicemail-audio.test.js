import test from 'node:test'
import assert from 'node:assert/strict'
import { createVoicemailContext } from './voicemail-audio.js'
import { createVoicemailPlayer } from './voicemail-player.js'

function audioHarness(t, close) {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'AudioContext')
  const contexts = []
  class Context {
    constructor() {
      this.state = 'suspended'
      this.closes = 0
      contexts.push(this)
    }
    resume() {
      this.state = 'running'
      return Promise.resolve()
    }
    close() {
      this.closes++
      this.state = 'closed'
      return close?.()
    }
  }
  Object.defineProperty(globalThis, 'AudioContext', { configurable: true, value: Context })
  t.after(() => {
    if (original) Object.defineProperty(globalThis, 'AudioContext', original)
    else delete globalThis.AudioContext
  })
  return contexts
}

test('音频上下文按需创建并复用，释放不重复关闭或创建上下文', (t) => {
  const contexts = audioHarness(t)
  const acquire = createVoicemailContext()
  acquire.dispose()
  assert.equal(contexts.length, 0)
  assert.equal(acquire(), acquire())
  assert.equal(contexts.length, 1)
  acquire.dispose()
  acquire.dispose()
  assert.equal(contexts.length, 1)
  assert.equal(contexts[0].closes, 1)
})

test('已关闭的音频上下文无需再次关闭', (t) => {
  const contexts = audioHarness(t)
  const acquire = createVoicemailContext()
  acquire().state = 'closed'
  acquire.dispose()
  assert.equal(contexts[0].closes, 0)
})

test('关闭音频资源的同步或异步失败不产生未处理异常', async (t) => {
  let close
  const contexts = audioHarness(t, () => close())
  for (close of [
    () => {
      throw new Error('SYNTHETIC-CLOSE')
    },
    () => Promise.reject(new Error('SYNTHETIC-CLOSE')),
  ]) {
    const acquire = createVoicemailContext()
    acquire()
    assert.doesNotThrow(() => acquire.dispose())
    acquire.dispose()
    await new Promise(setImmediate)
    assert.equal(contexts.at(-1).closes, 1)
  }
})

test('离开仍在下载的播放页面会关闭上下文，迟到的结果不会解码或重新创建声音', async (t) => {
  const contexts = audioHarness(t)
  let finishDownload,
    decoded = 0
  const player = createVoicemailPlayer({
    createContext: createVoicemailContext(),
    request: () => new Promise((resolve) => (finishDownload = resolve)),
    decode: () => {
      decoded++
    },
  })
  const pending = player.toggle({ id: 1, voicemail_id: 1 })
  player.dispose()
  player.dispose()
  finishDownload(new Blob(['#!AMR\nSYNTHETIC']))
  await pending
  assert.equal(contexts.length, 1)
  assert.equal(contexts[0].closes, 1)
  assert.equal(decoded, 0)
})

test('反复进入并离开播放页面，每个音频上下文均释放一次', async (t) => {
  const contexts = audioHarness(t)
  for (let i = 0; i < 100; i++) {
    const player = createVoicemailPlayer({
      createContext: createVoicemailContext(),
      request: async () => {
        throw new Error('SYNTHETIC-DOWNLOAD')
      },
    })
    await assert.rejects(player.toggle({ id: 1, voicemail_id: 1 }), /SYNTHETIC-DOWNLOAD/)
    player.dispose()
  }
  assert.equal(contexts.length, 100)
  assert.equal(
    contexts.every((context) => context.closes === 1),
    true,
  )
})
