import test from 'node:test'
import assert from 'node:assert/strict'
import { createOutboxFlow } from './outbox-flow.js'
function memory() {
  const m = new Map()
  return {
    getItem: (k) => m.get(k) || null,
    setItem: (k, v) => m.set(k, v),
    removeItem: (k) => m.delete(k),
  }
}
const ID = 'a'.repeat(32)
const preview = { id: ID, state: 'prepared', slot: 1, phone: '000', body: 'SYNTHETIC' }
const input = { slot: 1, phone: '000', body: 'SYNTHETIC' }
test('preview, confirmation and reload never repeat the POST', async () => {
  const storage = memory(),
    calls = []
  let saved = { ...preview }
  const request = async (path, method = 'GET', body) => {
    calls.push({ path, method, body })
    if (path.endsWith('/confirm')) saved = { ...saved, state: 'unknown', dispatched_at: 1 }
    return saved
  }
  const flow = createOutboxFlow({ request, storage, key: 'test', newID: () => ID })
  await flow.prepare(input)
  await assert.rejects(() => flow.confirm('001', true))
  await flow.confirm('000', true)
  await assert.rejects(() => flow.confirm('000', true))
  const restored = createOutboxFlow({ request, storage, key: 'test' })
  await restored.recover()
  assert.equal(restored.attempted, true)
  assert.equal(calls.filter((c) => c.path.endsWith('/confirm')).length, 1)
  assert.equal(calls.at(-1).method, 'GET')
  assert.equal(storage.getItem('test'), ID)
  assert.equal(storage.getItem('test:confirm:' + ID), ID)
})
test('lost confirmation response and even a prepared GET keep confirmation disabled', async () => {
  const storage = memory(),
    calls = []
  const request = async (path, method = 'GET') => {
    calls.push({ path, method })
    if (path.endsWith('/confirm')) throw new Error('lost response')
    return { ...preview }
  }
  const flow = createOutboxFlow({ request, storage, key: 'test', newID: () => ID })
  await flow.prepare(input)
  await assert.rejects(() => flow.confirm('000', true))
  await assert.rejects(() => flow.confirm('000', true))
  const restored = createOutboxFlow({ request, storage, key: 'test' })
  await restored.recover()
  await assert.rejects(() => restored.confirm('000', true))
  assert.equal(calls.filter((c) => c.path.endsWith('/confirm')).length, 1)
})
test('concurrent clicks and lost preview response retain one operation', async () => {
  const storage = memory(),
    calls = []
  let unblock
  const request = async (path, method = 'GET') => {
    calls.push({ path, method })
    if (path.endsWith('/prepare')) {
      await new Promise((resolve) => (unblock = resolve))
      throw new Error('offline')
    }
    return { ...preview }
  }
  const flow = createOutboxFlow({ request, storage, key: 'test', newID: () => ID })
  const first = assert.rejects(flow.prepare(input))
  await assert.rejects(() => flow.prepare(input))
  unblock()
  await first
  assert.equal(flow.id, ID)
  assert.equal(flow.record.id, ID)
  assert.equal(calls.filter((c) => c.method === 'POST').length, 1)
  await assert.rejects(() => flow.prepare(input))
  assert.throws(() => flow.release())
})
test('storage failure prevents POST; missing operation is never automatically replaced', async () => {
  let calls = 0
  const request = async () => {
    calls++
    throw new Error('missing')
  }
  const storage = {
    getItem: () => null,
    setItem: () => {
      throw new Error('blocked storage')
    },
  }
  const f = createOutboxFlow({ request, storage, key: 'test', newID: () => ID })
  await assert.rejects(() => f.prepare(input))
  assert.equal(calls, 0)
  const m = memory()
  m.setItem('test', ID)
  const recovered = createOutboxFlow({ request, storage: m, key: 'test' })
  await assert.rejects(() => recovered.recover())
  assert.equal(recovered.id, ID)
  assert.throws(() => recovered.release())
})

test('selecting another record never forgets the earlier confirmation attempt', async () => {
  const storage = memory()
  const request = async (path) => {
    if (path.endsWith('/confirm')) throw new Error('lost response')
    return { ...preview, id: path.endsWith('b'.repeat(32)) ? 'b'.repeat(32) : ID }
  }
  const f = createOutboxFlow({ request, storage, key: 'test', newID: () => ID })
  await f.prepare(input)
  await assert.rejects(() => f.confirm('000', true))
  await f.select('b'.repeat(32))
  await f.select(ID)
  assert.equal(f.attempted, true)
  await assert.rejects(() => f.confirm('000', true))
})

test('an older GET cannot overwrite a completed confirmation', async () => {
  let finish
  const request = async (path, method = 'GET') => {
    if (method === 'GET') return new Promise((resolve) => (finish = resolve))
    return { ...preview, state: path.endsWith('/confirm') ? 'submitted' : 'prepared' }
  }
  const f = createOutboxFlow({ request, storage: memory(), key: 'test', newID: () => ID })
  await f.prepare(input)
  const old = f.recover()
  await f.confirm('000', true)
  finish({ ...preview })
  await old
  assert.equal(f.record.state, 'submitted')
  assert.equal(f.attempted, true)
})

test('failed recovery removes stale preview eligibility but retains operation ID', async () => {
  const calls = []
  const request = async (path, method = 'GET') => {
    calls.push({ path, method })
    if (method === 'GET') throw new Error('offline')
    return { ...preview }
  }
  const f = createOutboxFlow({ request, storage: memory(), key: 'test', newID: () => ID })
  await f.prepare(input)
  await assert.rejects(f.recover())
  assert.equal(f.record, null)
  assert.equal(f.id, ID)
  await assert.rejects(f.confirm('000', true))
  assert.equal(calls.filter((c) => c.path.endsWith('/confirm')).length, 0)
})

test('wrong operation in a response cannot be confirmed', async () => {
  const calls = []
  const request = async (path, method = 'GET') => {
    calls.push({ path, method })
    return { ...preview, id: 'b'.repeat(32) }
  }
  const f = createOutboxFlow({ request, storage: memory(), key: 'test', newID: () => ID })
  await assert.rejects(f.prepare(input))
  assert.equal(f.record, null)
  assert.equal(f.id, ID)
  await assert.rejects(f.confirm('000', true))
  assert.equal(calls.filter((c) => c.path.endsWith('/confirm')).length, 0)
})
