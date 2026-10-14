// A malformed response is uncertain, even if its HTTP status looks successful.
// This helper never retries a request, including POSTs aborted by the timeout.
export function createRequest({
  getCredential,
  onUnauthorized,
  errors = {},
  fetcher = fetch,
  timeout = 15000,
}) {
  return async (path, method = 'GET', body) => {
    const credential = getCredential()
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeout)
    try {
      let response, data
      try {
        response = await fetcher('/api' + path, {
          method,
          signal: controller.signal,
          headers: {
            ...(credential ? { Authorization: 'Bearer ' + credential } : {}),
            ...(body ? { 'Content-Type': 'application/json' } : {}),
          },
          body: body ? JSON.stringify(body) : undefined,
        })
        if (response.status === 401) {
          if (credential === getCredential()) onUnauthorized()
          const e = new Error(credential ? '访问凭据不匹配，请重新输入。' : '请输入管理访问凭据。')
          e.status = 401
          throw e
        }
        if (credential !== getCredential()) throw new Error('identity changed')
        if (response.headers.get('Content-Type')?.split(';')[0].trim() !== 'application/json')
          throw new Error('invalid response')
        data = await response.json()
        if (credential !== getCredential()) throw new Error('identity changed')
        if (!data || typeof data !== 'object' || Array.isArray(data))
          throw new Error('invalid response')
      } catch (e) {
        if (e.status === 401) throw e
        throw new Error('未能确认服务响应，请查看原操作记录。不会自动重发。')
      }
      if (!response.ok) {
        const e = new Error(errors[data.error] || '操作未完成，请先查看原操作记录。')
        e.status = response.status
        e.code = data.error
        e.existingID = data.existing_id
        throw e
      }
      return data
    } finally {
      clearTimeout(timer)
    }
  }
}
