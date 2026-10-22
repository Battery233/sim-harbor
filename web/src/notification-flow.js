export const notificationErrors = {
  unknown: '结果未知，请在 iPhone 上核对',
  accepted: '推送成功',
  transport_failed: '无法完成推送连接',
  http_rejected: '推送服务拒绝请求',
  invalid_response: '推送服务返回内容无效',
  bark_rejected: 'Bark 未接受请求，请核对配置',
  push_request_failed: '推送请求未完成',
}
export function canRetryNotification(row, now) {
  return (
    row.source !== 'call' &&
    row.state === 'pending' &&
    row.failures > 0 &&
    row.next_at > now &&
    (!row.manual_retry_at || now - row.manual_retry_at >= 60)
  )
}
// No recovery path ever resubmits a POST. The server also persists cooldowns.
export function createNotificationActions({ request, now = () => Date.now() / 1000 }) {
  let busy = false,
    nextTest = 0
  async function run(action) {
    if (busy) throw new Error('通知操作正在处理，请稍后刷新。')
    busy = true
    try {
      return await action()
    } finally {
      busy = false
    }
  }
  return {
    test(confirmed) {
      return run(async () => {
        if (!confirmed) throw new Error('请先确认测试接收端。')
        if (now() < nextTest) throw new Error('请等待一分钟后再测试，并核对原结果。')
        nextTest = now() + 60
        return request('/notifications/test', 'POST', { confirmed: true })
      })
    },
    retry(id, confirmed) {
      return run(async () => {
        if (!confirmed || !Number.isSafeInteger(id) || id <= 0)
          throw new Error('请确认这条通知任务。')
        return request('/notifications/' + id + '/retry', 'POST', { confirmed: true })
      })
    },
  }
}
