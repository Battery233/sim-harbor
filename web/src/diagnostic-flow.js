export const diagnosticCategories = {
  service: '应用运行',
  call: '来电与录音',
  tcp: '板子连接',
  query: '命令与查询',
  sms: '短信收发',
  notification: '通知推送',
}
export const diagnosticOutcomes = {
  source_rejected: '上传来源不在允许范围',
  token_rejected: '上传令牌未通过校验',
  upload_busy: '上传过于频繁或并发已满',
  media_rejected: '音频格式、大小或上传参数不符合要求',
  capacity_bytes: '录音存储空间不足',
  capacity_files: '录音目录项数达到保护上限',
  capacity_records: '累计录音接收记录达到保护上限',
  reception_disabled: '应用接收服务未开启',
  duplicate: '重复上报已去重',
  deleted: '已删除录音重报，未恢复文件',
  unknown: '结果暂未确认',
  device_connection_changed: '板子连接已变化',
  call_ended: '原来电已结束',
  call_replaced: '原来电已被下一通代替',
  call_identity_insufficient: '原来电身份无法确认',
  stored: '入库事务已完成',
  store_failed: '入库事务失败',
  total_mismatch: '总片数与已保存记录不一致，已拒绝更新',
  invalid_fragment: '分片元数据无效，已拒绝更新',
  conflicting_fragment: '相同序号的分片内容冲突，已拒绝更新',
  reported_success: '板子报告本阶段成功',
  reported_failure: '板子报告本阶段失败',
  device_offline: '板子未连接',
  query_in_progress: '另一条查询正在进行',
  device_write_failed: '连接写入失败',
  device_query_failed: '板子未完成查询',
  device_result_unknown: '未等到结果',
  request_canceled: '查询请求已取消',
  transport_failed: '推送连接未完成',
  http_rejected: '推送服务拒绝请求',
  invalid_response: '推送服务返回内容无效',
  bark_rejected: 'Bark 未接受请求',
  accepted: '推送成功（Bark 已接受）',
}
export function diagnosticQuery({ level = '', category = '', reference = '' } = {}, before = 0) {
  const value = reference.trim().toLowerCase()
  if (
    !['', 'debug', 'info', 'warn', 'error'].includes(level) ||
    (category && !Object.hasOwn(diagnosticCategories, category)) ||
    (value && !/^(?:[a-f0-9]{24}|[a-f0-9]{32})$/.test(value)) ||
    !Number.isSafeInteger(before) ||
    before < 0
  )
    throw new Error('请填写有效的筛选条件：关联摘要为 24 位，发件操作 ID 为 32 位。')
  const query = new URLSearchParams()
  if (level) query.set('level', level)
  if (category) query.set('category', category)
  if (value) query.set(value.length === 32 ? 'operation_id' : 'ref', value)
  if (before) query.set('before', before)
  return query.toString()
}
function validate(data, exporting) {
  if (
    !data ||
    !Array.isArray(data.items) ||
    data.items.length > (exporting ? 5000 : 50) ||
    !data.status ||
    !Number.isSafeInteger(data.status.retained) ||
    !Number.isSafeInteger(data.status.limit) ||
    (exporting
      ? data.format_version !== 1 || data.scope !== 'recent_diagnostic_events'
      : !Number.isSafeInteger(data.next_before))
  )
    throw new Error('诊断响应不完整，请重新读取。')
  for (const item of data.items) {
    if (
      !Number.isSafeInteger(item.id) ||
      item.id <= 0 ||
      !Number.isSafeInteger(item.time_ms) ||
      typeof item.message !== 'string' ||
      typeof item.code !== 'string' ||
      !['debug', 'info', 'warn', 'error'].includes(item.level)
    )
      throw new Error('诊断记录结构无效。')
  }
  return data
}
// Every request is GET. Only the newest response may update the view/download;
// an export always fetches fresh data and never substitutes a cached list.
export function createDiagnosticFlow({ request, changed = () => {} }) {
  let generation = 0,
    stopped = false,
    activeFilter = ''
  let state = { items: [], data: null, next: 0, loading: false, error: '' }
  function publish() {
    if (!stopped) changed({ ...state })
  }
  async function run(filters, before, exporting) {
    const filterKey = diagnosticQuery(filters)
    if (!exporting && before && filterKey !== activeFilter) before = 0
    const query = diagnosticQuery(filters, before)
    const revision = ++generation
    if (stopped) return null
    state = {
      ...state,
      loading: true,
      error: '',
      ...(!before && !exporting ? { items: [], data: null, next: 0 } : {}),
    }
    publish()
    try {
      const data = validate(
        await request(
          '/diagnostics' + (exporting ? '/export' : '') + (query ? '?' + query : ''),
          'GET',
        ),
        exporting,
      )
      if (stopped || revision !== generation) return null
      if (!exporting) {
        activeFilter = filterKey
        state = {
          ...state,
          data,
          items: before ? [...state.items, ...data.items] : data.items,
          next: data.next_before,
        }
      }
      return data
    } catch (e) {
      if (!stopped && revision === generation) state = { ...state, error: e.message }
      return null
    } finally {
      if (!stopped && revision === generation) {
        state = { ...state, loading: false }
        publish()
      }
    }
  }
  return {
    load: (filters, before = 0) => run(filters, before, false),
    export: (filters) => run(filters, 0, true),
    dispose() {
      stopped = true
      generation++
    },
  }
}
