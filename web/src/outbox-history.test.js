import test from 'node:test'
import assert from 'node:assert/strict'
import {
  createOutboxHistory,
  outgoingCardLabel,
  outgoingFilterOptions,
  sendSlotOptions,
} from './outbox-history.js'

const a = '0123456789abcdef',
  b = 'fedcba9876543210'
const row = (sequence, card = a) => ({
  id: sequence.toString(16).padStart(32, '0'),
  sequence,
  card_id: card,
  slot: 1,
  state: 'prepared',
})
const page = (items, next_before = 0) => ({ items, next_before })

test('发件卡切换淘汰旧响应并重置分页，全部只读GET', async () => {
  const requests = [],
    pending = []
  let view
  const flow = createOutboxHistory({
    request: (path, method) => {
      requests.push({ path, method })
      return new Promise((resolve) => pending.push(resolve))
    },
    changed: (state) => (view = state),
  })
  const first = flow.load(a)
  pending.shift()(page([row(100)], 100))
  await first
  const earlier = flow.load(a, true)
  const other = flow.load(b, true)
  assert.equal(view.items.length, 0)
  pending[1](page([row(90, b)]))
  await other
  pending[0](page([row(80)]))
  await earlier
  assert.deepEqual(
    view.items.map((o) => o.card_id),
    [b],
  )
  assert.equal(requests[1].path, '/outgoing?card=' + a + '&before=100')
  assert.equal(requests[2].path, '/outgoing?card=' + b)
  assert(requests.every((r) => r.method === 'GET'))
})

test('刷新保留已加载历史，错误归属与关闭后响应不能混入', async () => {
  let view,
    response = page([row(3)], 3)
  const flow = createOutboxHistory({
    request: async () => response,
    changed: (state) => (view = state),
  })
  await flow.load(a)
  response = page([row(2)])
  await flow.load(a, true)
  response = page([row(4)])
  await flow.load(a)
  assert.deepEqual(
    view.items.map((o) => o.sequence),
    [4, 3, 2],
  )
  response = page([row(5, b)])
  await assert.rejects(flow.load(a), /归属/)
  assert.deepEqual(
    view.items.map((o) => o.sequence),
    [4, 3, 2],
  )
  response = page([row(6, a)])
  const stopped = flow.load(a)
  flow.dispose()
  await stopped
  assert.deepEqual(
    view.items.map((o) => o.sequence),
    [4, 3, 2],
  )
})

test('未关联卡不按槽或当前名称猜测，历史卡和号码同名仍按ID筛选', () => {
  const state = {
    online: true,
    status_current_session: true,
    card_locations_verified: true,
    status_received_at: 100,
    stale_seconds: 345,
    cards: [
      { id: a, name: 'Same name', number: 'TEST-NUMBER', current_slot: 2 },
      { id: b, name: 'Same name', number: 'TEST-NUMBER', current_slot: 0 },
    ],
  }
  const options = outgoingFilterOptions(state, 101)
  assert.equal(options[0].label, '全部发件记录')
  assert.notEqual(options[1].value, options[2].value)
  assert.notEqual(options[1].label, options[2].label)
  assert(options[2].label.includes('历史卡'))
  assert.equal(outgoingCardLabel({ slot: 1, card_tail: 'TEST' }), '未关联卡记录 · SIM编号尾号 TEST')
  assert.equal(
    outgoingCardLabel({
      card_id: a,
      card_current_name: 'Current name',
      card_number: 'TEST-NUMBER',
    }),
    'Current name · TEST-NUMBER',
  )
  assert(sendSlotOptions(state, 101)[1].label.includes('Same name'))
  assert(sendSlotOptions(state, 500).every((o) => o.label.includes('待核对')))
})

test('不匹配的未知来源结果与无效筛选不产生替代请求', async () => {
  let count = 0
  const flow = createOutboxHistory({
    request: async () => {
      count++
      return page([row(1)])
    },
  })
  await assert.rejects(flow.load('unknown'), /归属/)
  assert.equal(count, 1)
  await assert.rejects(flow.load('1'), /筛选/)
  assert.equal(count, 1)
})
