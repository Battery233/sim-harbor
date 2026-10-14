import test from 'node:test'
import assert from 'node:assert/strict'
import { createCardPushFlow } from './card-push-flow.js'
const card = { id: 'a'.repeat(16), notification_enabled: true }
test('卡推送按固定身份提交一次，失败只读重新确认，不自动补提交', async () => {
  const calls = []
  const flow = createCardPushFlow({
    request: async (path, method = 'GET') => {
      calls.push([path, method])
      if (method === 'PUT') throw Error('lost')
      return { cards: [] }
    },
  })
  await assert.rejects(flow.set(card, false))
  await flow.set(card, false)
  assert.equal(calls.length, 1)
  await flow.reload()
  assert.equal(calls[1][1], 'GET')
  assert(calls[0][0].includes(card.id))
})
test('卡身份无效和并发切换不发送，关闭页面丢弃结果', async () => {
  let resolve,
    count = 0,
    changed = 0
  const flow = createCardPushFlow({
    request: () => {
      count++
      return new Promise((r) => (resolve = r))
    },
    onChanged: () => changed++,
  })
  await assert.rejects(flow.set({ id: 'unknown' }, false))
  const first = flow.set(card, false)
  await flow.set(card, false)
  assert.equal(count, 1)
  flow.dispose()
  resolve({ card_id: card.id, notification_enabled: false })
  assert.equal(await first, null)
  assert.equal(changed, 0)
})

import { createRequest } from './request.js'
test('按卡推送复用实际请求适配器，PUT与JSON正文一致', async () => {
  const calls = []
  const request = createRequest({
    getCredential: () => 'SYNTHETIC',
    onUnauthorized: () => {},
    fetcher: async (path, options) => {
      calls.push([path, options])
      return new Response(JSON.stringify({ card_id: card.id, notification_enabled: false }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    },
  })
  const flow = createCardPushFlow({ request })
  await flow.set(card, false)
  assert.equal(calls.length, 1)
  assert.equal(calls[0][1].method, 'PUT')
  assert.deepEqual(JSON.parse(calls[0][1].body), { enabled: false, expected: true })
})
