import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { parse, compileScript } from '@vue/compiler-sfc'

// Render the actual row template without a DOM or running media/network APIs.
const filename = new URL('./CallEntry.vue', import.meta.url)
const { descriptor } = parse(await readFile(filename, 'utf8'))
const { content } = compileScript(descriptor, { id: 'call-entry-test', inlineTemplate: true })
const source = content.replace(/from\s+(['"])([^'"]+)\1/g, (_, _quote, specifier) => {
  const target = specifier.endsWith('.vue')
    ? 'data:text/javascript,export default { render() { return null } }'
    : specifier.startsWith('.')
      ? new URL(specifier, filename).href
      : import.meta.resolve(specifier)
  return 'from ' + JSON.stringify(target)
})
const { default: CallEntry } = await import(
  'data:text/javascript;base64,' + Buffer.from(source).toString('base64')
)
const call = {
  id: 1,
  phone: '',
  slot: 1,
  state: 'answered',
  voicemail_id: 1,
  voicemail_media_id: 'a'.repeat(32),
  voicemail_bytes: 100,
  voicemail_played_at: 0,
}
function render(overrides = {}) {
  return renderToString(createSSRApp(CallEntry, { call, cards: [], ...overrides }))
}

test('手动标记只在该条播放失败且未播放时出现，重试和已播放时隐藏', async () => {
  assert.doesNotMatch(await render(), /标记为已播放/)
  assert.match(await render({ playbackFailed: true }), /recording-mark-played.*?标记为已播放/s)
  for (const phase of ['loading', 'playing'])
    assert.doesNotMatch(
      await render({ playbackFailed: true, playback: { id: call.id, phase } }),
      /标记为已播放/,
    )
  assert.doesNotMatch(
    await render({ playbackFailed: true, call: { ...call, voicemail_played_at: 1234 } }),
    /标记为已播放/,
  )
  assert.match(
    await render({ playbackFailed: true, markingPlayed: true }),
    /recording-mark-played[^>]*disabled[^>]*>\s*保存中/s,
  )
})
