import test from 'node:test'
import assert from 'node:assert/strict'
import { diagnosticQuery, createDiagnosticFlow } from './diagnostic-flow.js'
const row = (id) => ({
  id,
  time_ms: 1700000000000,
  message: '合成诊断',
  code: 'tcp_bound',
  level: 'info',
})
const page = (id, next = 0) => ({
  items: [row(id)],
  next_before: next,
  status: { retained: 2, limit: 1000, level: 'info' },
})
const bundle = { ...page(1), format_version: 1, scope: 'recent_diagnostic_events' }

test('diagnostic filters accept only summaries/operation IDs and bounded cursors', () => {
  assert.equal(diagnosticQuery({ reference: 'a'.repeat(32) }), 'operation_id=' + 'a'.repeat(32))
  assert.equal(diagnosticQuery({ reference: ' B'.trim().repeat(24) }), 'ref=' + 'b'.repeat(24))
  for (const filter of [
    { reference: 'secret-message' },
    { category: 'unknown' },
    { level: 'trace' },
  ])
    assert.throws(() => diagnosticQuery(filter))
  assert.throws(() => diagnosticQuery({}, -1))
})

test('diagnostic paging and fresh export use only GET and never reuse cached data', async () => {
  const calls = [],
    states = []
  const flow = createDiagnosticFlow({
    request: async (...args) => {
      calls.push(args)
      return args[0].includes('/export')
        ? bundle
        : page(calls.length === 1 ? 2 : 1, calls.length === 1 ? 1 : 0)
    },
    changed: (state) => states.push(state),
  })
  await flow.load({})
  await flow.load({}, 1)
  const data = await flow.export({})
  assert.deepEqual(
    states.at(-1).items.map((r) => r.id),
    [2, 1],
  )
  assert.equal(data.format_version, 1)
  assert.deepEqual(calls, [
    ['/diagnostics', 'GET'],
    ['/diagnostics?before=1', 'GET'],
    ['/diagnostics/export', 'GET'],
  ])
})

test('older responses, downloads and unmounted views cannot override current filters', async () => {
  const pending = [],
    states = []
  const flow = createDiagnosticFlow({
    request: () => new Promise((resolve) => pending.push(resolve)),
    changed: (state) => states.push(state),
  })
  const oldExport = flow.export({})
  const recent = flow.load({ category: 'tcp' })
  pending[1](page(2))
  await recent
  pending[0](bundle)
  assert.equal(await oldExport, null)
  assert.equal(states.at(-1).items[0].id, 2)
  const old = flow.load({})
  const count = states.length
  flow.dispose()
  pending[2](page(1))
  await old
  assert.equal(states.length, count)
})

test('failed or malformed export does not produce a download or replay a request', async () => {
  let calls = 0,
    state
  const flow = createDiagnosticFlow({
    request: async () => {
      calls++
      if (calls === 1) throw new Error('offline')
      return page(1)
    },
    changed: (value) => (state = value),
  })
  assert.equal(await flow.export({}), null)
  assert.equal(calls, 1)
  assert.match(state.error, /offline/)
  assert.equal(await flow.export({}), null)
  assert.equal(calls, 2)
  assert.match(state.error, /不完整/)
})

test('changing filters before paging resets the cursor and does not mix categories', async () => {
  const calls = []
  let state
  const flow = createDiagnosticFlow({
    request: async (...args) => {
      calls.push(args)
      return page(calls.length === 1 ? 20 : 5, calls.length === 1 ? 20 : 0)
    },
    changed: (value) => (state = value),
  })
  await flow.load({ category: 'sms' })
  await flow.load({ category: 'tcp' }, 20)
  assert.deepEqual(calls[1], ['/diagnostics?category=tcp', 'GET'])
  assert.deepEqual(
    state.items.map((r) => r.id),
    [5],
  )
})
