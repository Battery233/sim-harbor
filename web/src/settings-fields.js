export const systemGroups = [
  {
    title: '管理访问',
    fields: [['admin_token_header', '自定义鉴权请求头（留空使用 Authorization）', 'text']],
  },
  {
    title: '管理网址与时间',
    fields: [
      ['public_url', '通知跳转的管理网址', 'text'],
      ['device_timezone', '记录解析与统计时区', 'text'],
    ],
  },
  {
    title: 'Bark与短信',
    fields: [
      ['bark_base_url', 'Bark服务器地址', 'text'],
      ['send_interval_seconds', '每卡发信最短间隔（秒）', 'number', 10, 86400],
      ['send_daily_limit', '每卡每天最多尝试', 'number', 1, 100],
      ['send_max_segments', '每条短信最多段数', 'number', 1, 10],
    ],
  },
  {
    title: '日志',
    fields: [
      ['log_max_bytes', '单个日志文件上限（字节）', 'number', 65536, 16777216],
      ['log_backups', '旧日志文件数量', 'number', 1, 5],
      ['diagnostics_limit', '网页最近诊断条数', 'number', 100, 5000],
      ['log_retention_days', '磁盘日志保留天数', 'number', 1, 30],
    ],
  },
  {
    title: '服务器本地备份',
    fields: [['backup_max_storage_mib', '服务器本地备份上限（MiB）', 'number', 0, 65536]],
  },
]
export const connectionFields = [
  [
    'device_id',
    '允许接入的设备编号',
    'text',
    '与板子上报的编号匹配；填错会拒绝上报，不会更改板子的编号。',
  ],
  [
    'http_address',
    '服务端网页监听地址',
    'text',
    '管理应用提供网页的地址；修改后可能需要通过新地址访问。',
  ],
  [
    'tcp_address',
    '服务端TCP监听地址',
    'text',
    '管理应用接收板子连接的地址；不会自动修改板子的连接目标。',
  ],
]
export const secretFields = [
  ['aes_key', 'AES Key（16字节）'],
  ['aes_iv', 'AES IV（16字节）'],
  ['admin_token', '管理访问凭据（至少24字符）'],
  ['bark_key', 'Bark Key'],
]
