import test from 'node:test'
import assert from 'node:assert/strict'
import { createAccessSession } from './access-session.js'
test('session access round trip and exit remove only the management credential', () => {
  const values = new Map([['other', 'kept']])
  const session = createAccessSession(() => ({
    getItem: (k) => values.get(k),
    setItem: (k, v) => values.set(k, v),
    removeItem: (k) => values.delete(k),
  }))
  assert.equal(session.read(), '')
  assert.equal(session.save('synthetic'), true)
  assert.equal(session.read(), 'synthetic')
  session.clear()
  assert.equal(session.read(), '')
  assert.equal(values.get('other'), 'kept')
})
test('restricted storage getter and operations cannot crash the access view', () => {
  for (const storage of [
    () => {
      throw new Error('blocked')
    },
    () => ({
      getItem() {
        throw new Error('blocked')
      },
      setItem() {
        throw new Error('blocked')
      },
      removeItem() {
        throw new Error('blocked')
      },
    }),
  ]) {
    const session = createAccessSession(storage)
    assert.equal(session.read(), '')
    assert.equal(session.save('synthetic'), false)
    assert.doesNotThrow(() => session.clear())
  }
})
