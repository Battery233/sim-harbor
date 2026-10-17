import test from 'node:test'
import assert from 'node:assert/strict'
import { createCallHistory, callerLabel, ringingLabel, callPushLabel } from './call-history.js'

const a = '0123456789abcdef',
  b = 'fedcba9876543210'
const call = (id, card = a) => ({
  id,
  card_id: card,
  slot: 1,
  phone: 'TEST-CALLER',
  state: 'missed',
  ring_seconds: 23,
})
const page = (items, next_before = 0) => ({ items, next_before })

test('匿名、未报告和未知时长明确区分，不把缺失当零秒', () => {
  assert.equal(callerLabel(' ANONYMOUS '), '匿名来电')
  assert.equal(callerLabel(''), '号码未提供')
  assert.equal(ringingLabel({ state: 'missed', ring_seconds: 0 }), '0 秒')
  assert.equal(ringingLabel({ state: 'missed', ring_seconds: 23 }), '23 秒')
  assert.equal(ringingLabel({ state: 'unknown', ring_seconds: 23 }), '时长未确认')
  assert.equal(ringingLabel({ state: 'missed', ring_seconds: null }), '时长未确认')
  assert.equal(ringingLabel({ state: 'ringing' }), '尚未结束')
  assert.equal(callPushLabel({ notification_state: 'restore_held' }), '恢复后待核对')
  assert.equal(
    callPushLabel({ notification_state: 'pending', notification_failures: 2 }),
    '等待重试',
  )
})

test('来电按固定卡过滤、分页、退役旧请求，全部GET', async () => {
  const pending = [],
    requests = []
  let view
  const history = createCallHistory({
    request: (path, method) => {
      requests.push({ path, method })
      return new Promise((resolve) => pending.push(resolve))
    },
    changed: (state) => (view = state),
  })
  const first = history.load({ card: a })
  pending.shift()(page([call(5)], 5))
  await first
  const older = history.load({ card: a }, true)
  const other = history.load({ card: b }, true)
  pending[1](page([call(4, b)]))
  await other
  pending[0](page([call(3)]))
  await older
  assert.deepEqual(
    view.items.map((c) => c.card_id),
    [b],
  )
  assert.equal(requests[1].path, '/calls?card=' + a + '&before=5')
  assert.equal(requests[2].path, '/calls?card=' + b)
  assert(requests.every((r) => r.method === 'GET'))
})

test('同一来电刷新更新结束状态，合并分页不重复条目', async () => {
  let data = page([{ ...call(3), state: 'ringing', ring_seconds: null }], 3),
    view
  const history = createCallHistory({ request: async () => data, changed: (s) => (view = s) })
  await history.load()
  data = page([call(2)])
  await history.load({}, true)
  data = page([call(3)])
  await history.load()
  assert.deepEqual(
    view.items.map((c) => c.id),
    [3, 2],
  )
  assert.equal(view.items[0].state, 'missed')
  assert.equal(view.items[0].ring_seconds, 23)
})

test('未知来源不会混入已知卡；关闭后的回复不展示，异常不重试', async () => {
  let count = 0,
    view,
    data = page([call(1, b)])
  const history = createCallHistory({
    request: async () => {
      count++
      return data
    },
    changed: (s) => (view = s),
  })
  await assert.rejects(history.load({ card: a }), /归属/)
  assert.equal(view.items.length, 0)
  assert.equal(count, 1)
  data = page([call(1, a)])
  const load = history.load({ card: a })
  history.dispose()
  await load
  assert.equal(view.items.length, 0)
})

test('通知详情链接校验记录编号与响应，不产生电话控制请求', async () => {
  const requests = []
  let data = call(8)
  const history = createCallHistory({
    request: async (path, method) => {
      requests.push({ path, method })
      return data
    },
  })
  assert.equal((await history.detail(8)).id, 8)
  data = call(9)
  await assert.rejects(history.detail(8), /不完整/)
  await assert.rejects(history.detail(0), /编号/)
  assert.equal(requests.length, 2)
  assert(requests.every((r) => r.method === 'GET' && r.path === '/calls/8'))
})

test('同号码汇总按稳定组ID更新，全历史次数不受分页影响', async () => {
  const { createCallGroups } = await import('./call-history.js')
  const group = (latest, count) => ({ id: 1, latest: call(latest), count, missed_count: count })
  let data = page([group(60, 60)], 60),
    view
  const requests = []
  const history = createCallGroups({
    request: async (path, method) => {
      requests.push({ path, method })
      return data
    },
    changed: (s) => {
      view = s
    },
  })
  await history.load({ card: a })
  data = page([{ id: 61, latest: call(61, b), count: 1, missed_count: 1 }])
  await assert.rejects(history.load({ card: a }, true), /归属/)
  data = page([group(62, 61)])
  await history.load({ card: a })
  assert.equal(view.items.length, 1)
  assert.equal(view.items[0].id, 1)
  assert.equal(view.items[0].count, 61)
  assert.equal(requests[0].path, '/call-groups?card=' + a)
  assert(requests.every((r) => r.method === 'GET'))
})

test('展开同号码用独立组分页，通知保持逐次详情，筛选不混用', async () => {
  const requests = []
  let data = page([call(60)], 60),
    view
  const history = createCallHistory({
    request: async (path, method) => {
      requests.push({ path, method })
      return data
    },
    changed: (s) => {
      view = s
    },
  })
  await history.load({ group: 1 })
  data = page([call(59)])
  await history.load({ group: 1 }, true)
  assert.equal(requests[1].path, '/calls?group=1&before=60')
  assert.equal(view.items.length, 2)
  data = call(1)
  assert.equal((await history.detail(1)).id, 1)
  assert.equal(requests[2].path, '/calls/1')
  await assert.rejects(history.load({ group: 1, card: a }), /筛选/)
  await assert.rejects(history.load({ group: -1 }), /筛选/)
  assert(requests.every((r) => r.method === 'GET'))
})

test('旧通知归入同卡同号码，匿名与不同卡不混入', async () => {
  const { groupContainsCall } = await import('./call-history.js')
  const group = { id: 1, latest: call(10) }
  assert.equal(groupContainsCall(group, call(5)), true)
  assert.equal(groupContainsCall(group, call(5, b)), false)
  assert.equal(groupContainsCall(group, { ...call(5), phone: 'anonymous' }), false)
  assert.equal(
    groupContainsCall(
      { id: 5, latest: { ...call(5), phone: 'anonymous' } },
      { ...call(5), phone: 'anonymous' },
    ),
    true,
  )
})

test('超时兜底与已确认未接的通知文案区分，结束后仍展示原提醒种类', () => {
  assert.equal(
    callPushLabel({ notification_state: 'sent', notification_kind: 'timeout' }),
    '推送成功 · 兜底提醒',
  )
  assert.equal(
    callPushLabel({
      notification_state: 'pending',
      notification_kind: 'timeout',
      notification_failures: 1,
    }),
    '等待重试 · 兜底提醒',
  )
  assert.equal(ringingLabel({ state: 'unknown', ring_seconds: null }), '时长未确认')
})
