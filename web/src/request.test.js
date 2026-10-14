import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequest } from './request.js'
const options = { getCredential: () => '', onUnauthorized: () => {} }
test('HTML login, malformed JSON and non-object JSON are uncertain rather than success', async () => {
  for (const response of [
    new Response('<html>login</html>'),
    new Response('{', { headers: { 'Content-Type': 'application/json' } }),
    new Response('null', { headers: { 'Content-Type': 'application/json' } }),
  ]) {
    const api = createRequest({ ...options, fetcher: async () => response })
    await assert.rejects(api('/outgoing/prepare', 'POST', {}), (e) => e.status === undefined)
  }
})
test('timeout aborts one request without replaying the POST', async () => {
  let calls = 0
  const api = createRequest({
    ...options,
    timeout: 1,
    fetcher: (_, { signal }) => {
      calls++
      return new Promise((_, reject) =>
        signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true }),
      )
    },
  })
  await assert.rejects(api('/outgoing/test/confirm', 'POST', {}))
  assert.equal(calls, 1)
})
test('old unauthorized response cannot lock a newly authenticated session', async () => {
  let credential = 'old',
    finish,
    locks = 0
  const api = createRequest({
    getCredential: () => credential,
    onUnauthorized: () => locks++,
    fetcher: () => new Promise((resolve) => (finish = resolve)),
  })
  const pending = api('/state')
  credential = 'new'
  finish(new Response('', { status: 401 }))
  await assert.rejects(pending, (e) => e.status === 401)
  assert.equal(locks, 0)
})

test('successful response from the old credential is discarded without replay', async () => {
  let credential = 'old',
    finish,
    calls = 0
  const api = createRequest({
    getCredential: () => credential,
    onUnauthorized: () => assert.fail('unexpected lock'),
    fetcher: () => {
      calls++
      return new Promise((resolve) => (finish = resolve))
    },
  })
  const pending = api('/state')
  credential = 'new'
  finish(new Response('{"old":true}', { headers: { 'Content-Type': 'application/json' } }))
  await assert.rejects(pending)
  assert.equal(calls, 1)
})
test('credential change while reading JSON discards the completed body', async () => {
  let credential = 'old',
    finish,
    reading
  const ready = new Promise((resolve) => (reading = resolve))
  const api = createRequest({
    getCredential: () => credential,
    onUnauthorized: () => {},
    fetcher: async () => ({
      status: 200,
      ok: true,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => {
        reading()
        return new Promise((resolve) => (finish = resolve))
      },
    }),
  })
  const pending = api('/messages')
  await ready
  credential = 'new'
  finish({ items: ['old'] })
  await assert.rejects(pending)
})
