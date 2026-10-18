// Fetch only the active header name before sending a management credential.
// Shared by API and file requests; never attach credentials to discovery.
export function createAuthHeaders({ fetcher = fetch, timeout = 10000 } = {}) {
  let pending
  function load() {
    if (pending) return pending
    const request = (async () => {
      const controller = new AbortController()
      const timer = setTimeout(() => controller.abort(), timeout)
      try {
        const response = await fetcher('/api/auth', {
          signal: controller.signal,
          credentials: 'same-origin',
          cache: 'no-store',
          redirect: 'error',
        })
        if (
          !response.ok ||
          response.headers.get('Content-Type')?.split(';')[0].trim() !== 'application/json'
        )
          throw new Error('invalid authentication configuration')
        const data = await response.json()
        if (
          !data ||
          Object.keys(data).length !== 1 ||
          typeof data.admin_token_header !== 'string' ||
          data.admin_token_header.length > 128 ||
          !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(data.admin_token_header)
        )
          throw new Error('invalid authentication header')
        return data.admin_token_header
      } catch {
        throw new Error('无法读取鉴权设置，请刷新页面后重试。')
      } finally {
        clearTimeout(timer)
      }
    })()
    pending = request
    request.catch(() => {
      if (pending === request) pending = undefined
    })
    return request
  }
  return {
    async get(credential) {
      const name = await load()
      return credential ? { [name]: 'Bearer ' + credential } : {}
    },
    reset() {
      pending = undefined
    },
  }
}
