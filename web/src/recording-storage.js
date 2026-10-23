export function checkedRecordingStorage(data) {
  if (
    !data ||
    !['files', 'bytes', 'limit_bytes'].every(
      (k) => Number.isSafeInteger(data[k]) && data[k] >= 0,
    ) ||
    typeof data.limited !== 'boolean' ||
    (data.complete !== undefined && typeof data.complete !== 'boolean')
  )
    throw new Error('录音空间统计不完整。')
  if (
    data.index_count !== undefined &&
    (!['index_count', 'deleted_count', 'index_limit', 'file_limit'].every(
      (k) => Number.isSafeInteger(data[k]) && data[k] >= 0,
    ) ||
      data.deleted_count > data.index_count ||
      data.index_limit <= 0 ||
      data.file_limit <= 0)
  )
    throw new Error('录音接收记录统计不完整。')
  return data
}

export function recordingStorageWarning(stats) {
  if (!stats) return ''
  if (stats.complete === false)
    return '目录统计已触及扫描保护边界，仅显示已确认的占用；请核对目录文件数量。'
  if (!stats.limited) return ''
  const items = [
    ['存储空间', stats.bytes, stats.limit_bytes],
    ['目录文件数量', stats.files, stats.file_limit],
    ['累计录音接收记录', stats.index_count, stats.index_limit],
  ]
  const full = items.filter(([, used, limit]) => limit > 0 && used >= limit).map(([name]) => name)
  if (full.length) return full.join('、') + '已达上限，将拒收新录音；已有录音保留。'
  const near = items
    .filter(([, used, limit]) => limit > 0 && used >= limit * 0.9)
    .map(([name]) => name)
  return near.length
    ? near.join('、') + '接近上限。删除文件会释放空间，累计接收记录仍为去重保留。'
    : ''
}
