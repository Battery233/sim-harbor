// Fetch the active token policy before sending a management credential.
// Shared by API and file requests; never attach credentials to discovery.
export function createAuthHeaders({ fetcher = fetch, timeout = 10000, onConfig } = {}) {
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
          Object.keys(data).some(
            (key) => !['admin_token_header', 'skip_admin_token'].includes(key),
          ) ||
          (Object.hasOwn(data, 'skip_admin_token') && typeof data.skip_admin_token !== 'boolean') ||
          typeof data.admin_token_header !== 'string' ||
          data.admin_token_header.length > 128 ||
          !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(data.admin_token_header)
        )
          throw new Error('invalid authentication header')
        const config = {
          admin_token_header: data.admin_token_header,
          skip_admin_token: data.skip_admin_token === true,
        }
        if (pending === request) onConfig?.(config)
        return config
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
      const config = await load()
      return credential && !config.skip_admin_token
        ? { [config.admin_token_header]: 'Bearer ' + credential }
        : {}
    },
    reset() {
      pending = undefined
    },
  }
}
