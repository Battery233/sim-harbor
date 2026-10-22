import test from 'node:test'
import assert from 'node:assert/strict'
import {
  createNotificationRecords,
  notificationKinds,
  notificationRecordLink,
  notificationCanceledReason,
} from './notification-records.js'
import { canRetryNotification } from './notification-flow.js'
const row = (source, id = 1) => ({
  id,
  key: source + ':' + id,
  source,
  kind: source === 'call' ? 'voicemail_missing' : 'sms',
  message_id: source === 'sms' ? id : undefined,
  call_id: source === 'call' ? id : undefined,
  slot: 1,
  state: 'pending',
  failures: 1,
  next_at: 200,
  manual_retry_at: 0,
  recorded_at: 100,
  accepted_at: 0,
  last_error: 'transport_failed',
})
const page = (items, next = '') => ({
  items,
  next_before: next,
  summary: { pending: items.length },
  enabled: true,
})
test('短信通话编号不冲突，类型和链接区分，通话不显示短信重试', async () => {
  let view
  const requests = []
  let data = page([row('call'), row('sms')], 'cursor')
  const flow = createNotificationRecords({
    request: async (...args) => {
      requests.push(args)
      return data
    },
    changed: (v) => (view = v),
  })
  await flow.load()
  assert.equal(view.items.length, 2)
  assert.equal(notificationRecordLink(view.items[0]), '?call=1')
  assert.equal(notificationRecordLink(view.items[1]), '?message=1')
  assert.equal(canRetryNotification(row('call'), 100), false)
  assert.equal(canRetryNotification(row('sms'), 100), true)
  assert.equal(notificationKinds.voicemail_late, '迟到录音已收到')
  data = page([row('call', 2)])
  await flow.load({}, true)
  assert.equal(view.items.length, 3)
  assert.match(requests[1][0], /before=cursor/)
  assert(requests.every((r) => r[1] === 'GET'))
  data = page([row('call', 3)])
  await flow.load({ source: 'call' })
  assert.equal(view.items.length, 1)
  assert.match(requests[2][0], /type=call/)
  assert.equal(requests[2][0].includes('before'), false)
})
test('旧筛选和关闭页面后的响应不覆盖新列表；错误类型拒绝展示', async () => {
  let finish, view
  const flow = createNotificationRecords({
    request: (path) =>
      path.includes('type=sms')
        ? new Promise((resolve) => (finish = resolve))
        : Promise.resolve(page([row('call')])),
    changed: (v) => (view = v),
  })
  const old = flow.load({ source: 'sms' })
  await flow.load({ source: 'call' })
  finish(page([row('sms')]))
  assert.equal(await old, false)
  assert.equal(view.items[0].source, 'call')
  const bad = createNotificationRecords({ request: async () => page([row('sms')]) })
  await assert.rejects(bad.load({ source: 'call' }), /筛选/)
  flow.dispose()
  assert.equal(await flow.load(), false)
})
test('取消文案按原因区分，录音迟到不再重试过时的缺失提醒', () => {
  assert.match(notificationCanceledReason({ last_error: 'voicemail_arrived' }), /已收到/)
  assert.match(notificationCanceledReason({ last_error: 'voicemail_deleted' }), /录音已删除/)
  assert.match(
    notificationCanceledReason({ last_error: 'card_notifications_disabled' }),
    /通知已关闭/,
  )
})

test('旧请求失败或离开页面后不显示过时错误', async () => {
  let reject
  const flow = createNotificationRecords({ request: () => new Promise((_, r) => (reject = r)) })
  const pending = flow.load()
  flow.dispose()
  reject(new Error('expired'))
  assert.equal(await pending, false)
})
