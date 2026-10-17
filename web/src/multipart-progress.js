export function multipartProgress(message) {
  if (!message || (!message.multipart && message.complete !== false)) return null
  const total =
    Number.isInteger(message.parts_total) && message.parts_total > 0 && message.parts_total <= 255
      ? message.parts_total
      : 0
  if (total === 1) return null
  const indices = [
    ...new Set(
      (Array.isArray(message.part_indices) ? message.part_indices : []).filter(
        (n) => Number.isInteger(n) && n >= 1 && n <= 255 && (!total || n <= total),
      ),
    ),
  ].sort((a, b) => a - b)
  const complete = message.complete === true
  const count = complete && total ? total : indices.length
  let label
  if (complete) label = total ? `已收齐 · ${total}/${total}` : '已收齐 · 总片数未知'
  else if (total && count) label = `等待分片 · ${count}/${total}`
  else if (total) label = `等待分片 · 共 ${total} 片`
  else label = count ? `等待分片 · 已保存 ${count} 片` : '等待分片 · 总片数未知'
  let detail
  if (complete) detail = total ? `已收齐 ${total} 片。` : '已标记完整，旧记录未保存总片数。'
  else if (total && count) {
    const missing = Array.from({ length: total }, (_, i) => i + 1).filter(
      (n) => !indices.includes(n),
    )
    detail = missing.length
      ? `预计 ${total} 片，已保存 ${count} 片。已收到第 ${indices.join('、')} 片；缺少第 ${missing.join('、')} 片。`
      : `已保存全部 ${total} 个分片序号，完整状态仍待核对。`
  } else if (total) detail = `预计 ${total} 片；尚未记录到可计数的分片序号，无法确认已收片数。`
  else
    detail = count
      ? `已保存 ${count} 片（第 ${indices.join('、')} 片），总片数未知。`
      : '总片数及已收片数尚未记录。'
  return { label, detail }
}
