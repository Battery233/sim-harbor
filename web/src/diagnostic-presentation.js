import { diagnosticOutcomes } from './diagnostic-flow.js'

const queryCodes = new Set(['query_started', 'query_completed', 'query_failed'])
const titles = {
  call_report: '收到来电状态上报',
  voicemail_rejected: '录音上传未接受',
  voicemail_capacity: '录音接收触及保护限制',
  voicemail_stored: '录音上传已处理',
  sms_fragment_received: '收到长短信上报，正在保存',
  sms_fragment_stored: '长短信上报已处理',
  sms_fragment_rejected: '长短信上报未能保存',
  service_started: '管理应用已启动',
  service_stopped: '管理应用已停止',
  configuration_rejected: '应用配置有误',
  database_startup_failed: '应用无法打开数据库',
  tcp_listener_failed: '应用无法接收板子连接',
  http_listener_failed: '管理网页无法启动',
  tcp_connected: '板子正在连接',
  tcp_bound: '板子已连接到应用',
  tcp_peer_closed: '板子断开了连接',
  tcp_idle_timeout: '长时间没有收到板子消息',
  tcp_source_rejected: '连接来自未允许的地址',
  tcp_identity_rejected: '设备编号不符合配置',
  tcp_session_replaced: '板子建立了新连接',
  tcp_session_retired: '旧连接已结束',
  tcp_shutdown: '应用停止，板子连接已关闭',
  tcp_decode_failed: '无法解密板子的消息',
  tcp_parse_failed: '无法识别板子的消息格式',
  tcp_frame_invalid: '板子消息不完整或过大',
  tcp_read_failed: '板子连接读取失败',
  handshake_write_failed: '连接初始化应答未发出',
  event_persistence_failed: '板子消息未能保存',
  event_received: '已收到并保存板子消息',
  sms_preview_created: '短信预览已保存，尚未发送',
  sms_dispatch_claimed: '正在准备提交短信指令',
  sms_write_completed: '短信发送指令已提交',
  sms_write_unknown: '短信指令是否发出尚不确定',
  sms_result_persistence_failed: '短信发送结果未能保存',
  sms_report_received: '收到短信发送进度',
  notification_attempt: '正在向推送服务提交通知',
  notification_accepted: '推送服务已接受通知',
  notification_failed: '通知推送未成功确认',
  notification_queue_failed: '无法读取待推送通知',
  notification_persistence_failed: '通知结果未能保存',
}

function queryKey(row) {
  return typeof row.transaction === 'string' && /^[a-f0-9]{24}$/.test(row.transaction)
    ? JSON.stringify([row.session || '', row.transaction])
    : null
}

function isQueryStage(row) {
  return (
    queryCodes.has(row.code) ||
    (row.code === 'command_response' &&
      (row.event_type === 401 || (row.event_type === 402 && row.sub_type === 46)))
  )
}

function repeatKey(row) {
  return JSON.stringify([
    row.code,
    row.level,
    row.message,
    row.outcome,
    row.event_type,
    row.sub_type,
    row.slot,
    row.session,
    row.transaction,
    row.operation,
  ])
}

function describe(row) {
  if (row.code === 'query_completed')
    return { title: '状态查询成功', explanation: '应用已收到这次查询对应的板子状态。' }
  if (row.code === 'query_started')
    return { title: '已开始查询状态', explanation: '此处尚未记录到这次查询的最终结果。' }
  if (row.code === 'query_failed')
    return {
      title: '状态查询未完成',
      explanation: diagnosticOutcomes[row.outcome] || '未能确认查询结果，请查看技术详情。',
    }
  if (row.code === 'command_response') {
    if (row.outcome === 'reported_failure')
      return {
        title: '板子报告命令失败',
        explanation: '具体原因需核对板子的接口日志；这条回执本身不表示短信已发送。',
      }
    if (row.outcome === 'reported_success' && row.event_type === 401)
      return {
        title: '板子已接受命令',
        explanation: '还需等待处理结果；接受不代表短信已发出或送达。',
      }
    if (row.outcome === 'reported_success' && row.event_type === 402) {
      if (row.sub_type === 46)
        return {
          title: '板子返回了状态信息',
          explanation: '是否匹配本次查询，以状态查询结果为准。',
        }
      if (row.sub_type === 11) return { title: '板子已完成时间同步', explanation: '' }
      return {
        title: '板子报告命令处理成功',
        explanation: '短信是否送达请查看发件箱，不能仅凭命令回执判断。',
      }
    }
    return { title: '板子返回了命令回执', explanation: '尚不能确认这条命令的最终结果。' }
  }
  const explanations = {
    voicemail_rejected: diagnosticOutcomes[row.outcome] || '请核对接收配置或板子日志。',
    voicemail_capacity: diagnosticOutcomes[row.outcome] || '录音接收达到保护上限。',
    voicemail_stored:
      diagnosticOutcomes[row.outcome] || '录音已保存；是否关联请查看来电或未关联录音。',
    sms_fragment_received: '这条记录只表示收到上报，保存结果请看后续记录。',
    sms_fragment_stored: '分片是否收齐请查看收件箱中的组装进度。',
    sms_fragment_rejected: diagnosticOutcomes[row.outcome] || '已有分片仍保留，请查看技术详情。',
    tcp_decode_failed: '请核对板子与应用的加密模式、密钥和消息格式。',
    tcp_bound: '这表示历史连接事件，当前是否在线请看首页。',
    sms_dispatch_claimed: '操作已保存，应用不会自动重复下发。',
    sms_write_completed: '是否成功或送达请查看发件箱。',
    sms_write_unknown: '应用不会自动重发，请先核对原操作的结果。',
    sms_result_persistence_failed: '请核对原操作，应用不会因此补发短信。',
    sms_report_received: '提交与送达是不同阶段，最终结果请看发件箱。',
    notification_accepted: '请在 iPhone 上确认是否收到。',
    notification_failed: diagnosticOutcomes[row.outcome] || '请查看“推送记录”中的任务结果。',
    notification_persistence_failed: '后续重试可能重复通知，请查看“推送记录”。',
  }
  return {
    title: Object.hasOwn(titles, row.code) ? titles[row.code] : row.message,
    explanation: Object.hasOwn(explanations, row.code) ? explanations[row.code] : '',
  }
}

// Group only loaded evidence. Preserve every source row for details and exports.
export function diagnosticRows(items) {
  const ordered = [...items].sort((a, b) => b.id - a.id)
  const anchors = new Set(
    ordered
      .filter((r) => queryCodes.has(r.code))
      .map(queryKey)
      .filter(Boolean),
  )
  const queries = new Map()
  const groups = []
  let repeated = null
  for (const row of ordered) {
    const key = queryKey(row)
    if (key && anchors.has(key) && isQueryStage(row)) {
      let group = queries.get(key)
      if (!group) {
        group = { id: row.id, kind: 'query', records: [] }
        queries.set(key, group)
        groups.push(group)
      }
      group.records.push(row)
      repeated = null
      continue
    }
    if (
      row.code === 'command_response' &&
      repeated &&
      repeatKey(repeated.records[0]) === repeatKey(row) &&
      Math.abs(repeated.records[0].time_ms - row.time_ms) <= 5000
    ) {
      repeated.records.push(row)
    } else {
      const group = { id: row.id, kind: 'event', records: [row] }
      groups.push(group)
      repeated = row.code === 'command_response' ? group : null
    }
  }
  return groups.map((group) => {
    const row =
      group.records.find((r) => r.code === 'query_completed' || r.code === 'query_failed') ||
      group.records.find((r) => r.code === 'query_started') ||
      group.records[0]
    const warning =
      row.level === 'error' || row.level === 'warn' || row.outcome === 'reported_failure'
    return {
      ...group,
      ...describe(row),
      time_ms: group.records[0].time_ms,
      category: row.category,
      warning,
      status: warning ? '需留意' : row.code === 'query_completed' ? '已完成' : '记录',
      duration_ms: row.duration_ms,
    }
  })
}
