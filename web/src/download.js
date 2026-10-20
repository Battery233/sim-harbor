// Binary/text downloads share the management credential and never retry.
// A replaced credential, canceled view or incomplete response cannot download.
export function createFileRequest({
  getCredential,
  onUnauthorized,
  getAuthHeaders,
  fetcher = fetch,
  timeout = 60000,
}) {
  return async (path, method = 'GET', body, options = {}) => {
    const credential = getCredential()
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeout)
    try {
      const authHeaders = getAuthHeaders
        ? await getAuthHeaders(credential)
        : credential
          ? { Authorization: 'Bearer ' + credential }
          : {}
      if (credential !== getCredential()) throw new Error('访问身份已变化，请重新操作。')
      const response = await fetcher('/api' + path, {
        method,
        signal: controller.signal,
        redirect: 'error',
        headers: {
          ...authHeaders,
          ...(body ? { 'Content-Type': 'application/json' } : {}),
        },
        body: body ? JSON.stringify(body) : undefined,
      })
      if (credential !== getCredential()) throw new Error('访问身份已变化，请重新操作。')
      if (response.status === 401) {
        onUnauthorized()
        throw new Error('请输入有效的管理访问凭据。')
      }
      if (!response.ok) {
        let code = ''
        try {
          code = (await response.json()).error
        } catch {
          /* No raw server text in the UI. */
        }
        const messages = {
          export_limit_exceeded: '超过导出上限，请缩小日期或卡范围。',
          invalid_filter: '导出条件无效。',
          export_in_progress: '另一份收件导出正在生成，请稍后。',
          backup_in_progress: '另一份备份正在生成，请稍后。',
          backup_limit_exceeded: '数据库超过当前备份容量上限。',
          backup_confirmation_required: '请确认下载包含私人短信的备份。',
        }
        throw new Error(messages[code] || '文件未生成，请重新检查服务状态。')
      }
      const type = response.headers.get('Content-Type')?.split(';')[0].trim()
      const allowed = options.types || ['application/json', 'text/csv', 'application/zip']
      if (!allowed.includes(type)) throw new Error('文件响应类型无效。')
      if (!response.headers.get('Content-Disposition')?.startsWith('attachment;'))
        throw new Error('文件响应不完整。')
      const length = response.headers.get('Content-Length')
      const max =
        Number.isSafeInteger(options.max) && options.max > 0
          ? options.max
          : type === 'application/zip'
            ? 272 * 1024 * 1024
            : 32 * 1024 * 1024
      if (!length || !/^\d+$/.test(length) || Number(length) > max)
        throw new Error('文件大小无效。')
      const reader = response.body.getReader()
      const chunks = []
      let size = 0
      try {
        while (true) {
          const { value, done } = await reader.read()
          if (done) break
          size += value.byteLength
          if (size > max || size > Number(length)) throw new Error('文件大小超出声明范围。')
          chunks.push(value)
        }
      } catch (e) {
        await reader.cancel().catch(() => {})
        throw e
      } finally {
        reader.releaseLock()
      }
      if (size !== Number(length) || credential !== getCredential())
        throw new Error('下载不完整或访问身份已变化。')
      return new Blob(chunks, { type })
    } finally {
      clearTimeout(timer)
    }
  }
}

export function messageExportQuery({
  slot = '0',
  card = '',
  phone = '',
  from = '',
  until = '',
  format = 'json',
}) {
  if (
    !['0', '1', '2'].includes(String(slot)) ||
    (card !== '' && card !== 'unknown' && !/^[a-f0-9]{16}$/.test(card)) ||
    (card !== '' && String(slot) !== '0') ||
    !['json', 'csv'].includes(format) ||
    phone.length > 64
  )
    throw new Error('请填写有效的导出条件。')
  const query = new URLSearchParams({ format })
  if (card) query.set('card', card)
  else query.set('slot', String(slot))
  if (phone.trim()) query.set('phone', phone.trim())
  let start = 0,
    end = 0
  for (const [key, value] of [
    ['from', from],
    ['until', until],
  ]) {
    if (!value) continue
    const seconds = new Date(value).getTime() / 1000
    if (!Number.isInteger(seconds) || seconds <= 0 || seconds > 4102444800)
      throw new Error('日期无效。')
    query.set(key, String(seconds))
    if (key === 'from') start = seconds
    else end = seconds
  }
  if (end && start >= end) throw new Error('结束时间必须晚于开始时间。')
  return query.toString()
}
export function createDownloadFlow({ request, save }) {
  let version = 0,
    stopped = false
  return {
    async run(path, filename, method = 'GET', body) {
      if (stopped) return false
      const revision = ++version
      const blob = await request(path, method, body)
      if (stopped || revision !== version) return false
      save(blob, filename)
      return true
    },
    dispose() {
      stopped = true
      version++
    },
  }
}
export function saveFile(blob, filename) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.append(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
