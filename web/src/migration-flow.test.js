import test from 'node:test'
import assert from 'node:assert/strict'
import { createMigrationFlow } from './migration-flow.js'
const checks = {
  source_stopped: true,
  history_complete: true,
  unknown_acknowledged: true,
  backup_saved: true,
}
const blocked = {
  restored: true,
  blocked: true,
  confirmed_at: 0,
  unknown_outgoing: 1,
  revision: 'a'.repeat(32),
}
const preview = {
  migration: blocked,
  token: 'b'.repeat(24),
  backup_sha256: 'c'.repeat(64),
  expires_at: Math.floor(Date.now() / 1000) + 900,
}
test('migration requires explicit consent, preview and confirmation, never sends SMS', async () => {
  const calls = []
  let state
  const flow = createMigrationFlow({
    changed: (s) => (state = s),
    request: async (path, method, body) => {
      calls.push({ path, method, body })
      return path.endsWith('/preview')
        ? preview
        : path.endsWith('/confirm')
          ? { ...blocked, blocked: false, confirmed_at: 1 }
          : blocked
    },
  })
  await flow.load()
  await flow.preview({})
  assert.equal(calls.length, 1)
  await flow.preview(checks)
  await flow.confirm(checks, false)
  assert.equal(calls.length, 2)
  await flow.load()
  await flow.preview(checks)
  await flow.confirm(checks, true)
  assert.equal(state.status.blocked, false)
  assert.equal(calls.filter((c) => c.path.endsWith('/confirm')).length, 1)
  assert(calls.every((c) => c.path.startsWith('/migration')))
})
test('lost confirmation never retries and permits only GET until persisted state is read', async () => {
  const calls = []
  let state
  const flow = createMigrationFlow({
    changed: (s) => (state = s),
    request: async (path) => {
      calls.push(path)
      if (path.endsWith('/confirm')) throw Error('response lost')
      return path.endsWith('/preview') ? preview : { ...blocked, blocked: false, confirmed_at: 1 }
    },
  })
  await flow.preview(checks)
  await flow.confirm(checks, true)
  assert.equal(state.uncertain, true)
  await flow.confirm(checks, true)
  await flow.preview(checks)
  assert.equal(calls.filter((p) => p.endsWith('/confirm')).length, 1)
  await flow.load()
  assert.equal(state.uncertain, false)
  assert.equal(state.status.confirmed_at, 1)
})
test('invalid or expired preview is never submitted and a closed view stops requests', async () => {
  const calls = []
  const flow = createMigrationFlow({
    request: async (p) => {
      calls.push(p)
      return { ...preview, token: 'invalid' }
    },
  })
  await flow.preview(checks)
  await flow.confirm(checks, true)
  assert.deepEqual(calls, ['/migration/preview'])
  flow.dispose()
  await flow.load()
  assert.equal(calls.length, 1)
})
