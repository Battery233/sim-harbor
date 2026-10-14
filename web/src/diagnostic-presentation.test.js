import test from 'node:test'
import assert from 'node:assert/strict'
import { diagnosticRows } from './diagnostic-presentation.js'

const session = 'a'.repeat(24)
const transaction = 'b'.repeat(24)
function row(id, code, extra = {}) {
  return Object.freeze({
    id,
    code,
    level: 'info',
    category: 'query',
    message: '合成原始说明',
    time_ms: 1700000000000 + id,
    session,
    transaction,
    ...extra,
  })
}

test('同一次查询汇总成功结果并保留全部原始证据，加载更早页后补齐阶段', () => {
  const items = Object.freeze([
    row(3, 'query_completed', { duration_ms: 24 }),
    row(2, 'command_response', { event_type: 402, sub_type: 46, outcome: 'reported_success' }),
    row(1, 'query_started'),
  ])
  const first = diagnosticRows(items.slice(0, 1))
  assert.equal(first[0].title, '状态查询成功')
  const all = diagnosticRows(items)
  assert.equal(all.length, 1)
  assert.equal(all[0].title, '状态查询成功')
  assert.equal(all[0].duration_ms, 24)
  assert.equal(all[0].warning, false)
  assert.deepEqual(all[0].records, items)
  assert.deepEqual(
    items.map((r) => r.id),
    [3, 2, 1],
  )
})

test('缺失关联、不同会话或不同事务的查询不能混合', () => {
  const items = [
    row(5, 'query_completed'),
    row(4, 'query_started', { session: 'c'.repeat(24) }),
    row(3, 'query_started', { transaction: 'd'.repeat(24) }),
    row(2, 'query_started', { transaction: undefined }),
    row(1, 'query_completed', { transaction: undefined }),
  ]
  const all = diagnosticRows(items)
  assert.equal(all.length, 5)
  assert.equal(all[1].title, '已开始查询状态')
  assert.deepEqual(
    all.flatMap((g) => g.records),
    items,
  )
})

test('失败或接受回执不被展示成查询完成、短信送达或自动重发', () => {
  const failure = diagnosticRows([
    row(3, 'query_failed', { outcome: 'device_result_unknown', level: 'warn' }),
    row(2, 'command_response', { event_type: 401, outcome: 'reported_success' }),
    row(1, 'query_started'),
  ])[0]
  assert.equal(failure.title, '状态查询未完成')
  assert.equal(failure.explanation, '未等到结果')
  assert.equal(failure.warning, true)
  const accepted = diagnosticRows([
    row(1, 'command_response', { event_type: 401, sub_type: 16, outcome: 'reported_success' }),
  ])[0]
  assert.equal(accepted.title, '板子已接受命令')
  assert.match(accepted.explanation, /不代表短信已发出或送达/)
  const processed = diagnosticRows([
    row(1, 'command_response', { event_type: 402, sub_type: 16, outcome: 'reported_success' }),
  ])[0]
  assert.equal(processed.title, '板子报告命令处理成功')
  assert.match(processed.explanation, /不能仅凭命令回执判断/)
  const unknown = diagnosticRows([row(1, 'sms_write_unknown')])[0]
  assert.match(unknown.explanation, /不会自动重发/)
})

test('近时间重复错误只合并回执数，保留每条记录并提示失败', () => {
  const items = Array.from({ length: 23 }, (_, i) =>
    row(23 - i, 'command_response', {
      event_type: 401,
      sub_type: 0,
      outcome: 'reported_failure',
      transaction: undefined,
    }),
  )
  const all = diagnosticRows(items)
  assert.equal(all.length, 1)
  assert.equal(all[0].title, '板子报告命令失败')
  assert.equal(all[0].records.length, 23)
  assert.equal(all[0].warning, true)
  assert.deepEqual(all[0].records, items)
})

test('不同操作、结果、会话、时间段或被其他事件隔开的回执不合并', () => {
  const base = { event_type: 401, outcome: 'reported_failure', transaction: undefined }
  for (const extra of [
    { operation: 'd'.repeat(32) },
    { session: 'c'.repeat(24) },
    { transaction: 'd'.repeat(24) },
    { outcome: 'reported_success' },
    { time_ms: 1700000010000 },
    { sub_type: 46 },
    { slot: 2 },
  ]) {
    assert.equal(
      diagnosticRows([
        row(2, 'command_response', base),
        row(1, 'command_response', { ...base, ...extra }),
      ]).length,
      2,
    )
  }
  // Even a query stage already grouped elsewhere must break adjacent repetition.
  const all = diagnosticRows([
    row(5, 'query_completed'),
    row(4, 'command_response', base),
    row(3, 'query_started'),
    row(2, 'command_response', base),
    row(1, 'tcp_connected', { category: 'tcp' }),
  ])
  assert.equal(all.length, 4)
  assert.equal(all.filter((g) => g.kind === 'event' && g.records.length > 1).length, 0)
  assert.equal(all.flatMap((g) => g.records).length, 5)
})

test('状态回执、校时与未知事件各自保留事实边界，显示不篡改原消息', () => {
  const all = diagnosticRows([
    row(3, 'command_response', { event_type: 402, sub_type: 46, outcome: 'reported_success' }),
    row(2, 'command_response', { event_type: 402, sub_type: 11, outcome: 'reported_success' }),
    row(1, 'new_event', { message: '未来新增的合成事件' }),
  ])
  assert.equal(all[0].title, '板子返回了状态信息')
  assert.notEqual(all[0].status, '已完成')
  assert.equal(all[1].title, '板子已完成时间同步')
  assert.equal(all[2].title, '未来新增的合成事件')
  assert.equal(all[0].records[0].message, '合成原始说明')
})
