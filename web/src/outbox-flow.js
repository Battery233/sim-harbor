// Persist only operation IDs, never phone numbers, SMS bodies or credentials.
export function operationID() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) =>
    b.toString(16).padStart(2, '0'),
  ).join('')
}
export function createOutboxFlow({ request, storage, key, newID = operationID }) {
  let id = storage.getItem(key) || ''
  let attempted = id !== '' && storage.getItem(key + ':confirm:' + id) === id
  let record = null
  let busy = false
  let revision = 0
  function checked(found) {
    if (
      !found ||
      found.id !== id ||
      ![1, 2].includes(found.slot) ||
      typeof found.phone !== 'string' ||
      typeof found.body !== 'string' ||
      !phaseLabels[found.state]
    )
      throw new Error('原操作返回内容不匹配，请重新查看记录；已禁用确认。')
    return found
  }
  function save(next) {
    storage.setItem(key, next) // Abort before POST if persistence is unavailable.
    id = next
    attempted = storage.getItem(key + ':confirm:' + id) === id
    record = null
    revision++
  }
  async function recover(internal = false) {
    if (busy && !internal) return record
    if (!id) return null
    const original = id
    const version = ++revision
    try {
      const found = await request('/outgoing/' + original)
      if (original === id && version === revision) record = checked(found)
    } catch (e) {
      if (original !== id || version !== revision) return record
      record = null
      throw e
    }
    return record
  }
  async function exclusive(action) {
    if (busy) throw new Error('操作正在处理，请稍后查看记录。')
    busy = true
    revision++ // Retire GETs that began before this mutation.
    try {
      return await action()
    } finally {
      busy = false
    }
  }
  return {
    get id() {
      return id
    },
    get record() {
      return record
    },
    get attempted() {
      return attempted
    },
    recover: () => recover(),
    async select(next) {
      return exclusive(async () => {
        save(next)
        return recover(true)
      })
    },
    async prepare(input) {
      return exclusive(async () => {
        if (id) throw new Error('请先查看并结束原操作，避免重复发送。')
        save(newID())
        try {
          record = checked(await request('/outgoing/prepare', 'POST', { ...input, id }))
          return record
        } catch (e) {
          if (e.existingID) {
            save(e.existingID)
            await recover(true).catch(() => {})
          } else if (e.status) {
            // This endpoint never sends. An explicit rejection can discard the preview ID.
            storage.removeItem(key)
            id = ''
            record = null
          } else {
            await recover(true).catch(() => {})
          }
          throw e
        }
      })
    },
    async confirm(phone, consent) {
      return exclusive(async () => {
        if (
          !record ||
          record.state !== 'prepared' ||
          attempted ||
          !consent ||
          phone !== record.phone
        )
          throw new Error('请核对原操作、目标号码和费用确认。')
        storage.setItem(key + ':confirm:' + id, id)
        attempted = true // A lost HTTP response never re-enables sending.
        try {
          record = checked(
            await request('/outgoing/' + id + '/confirm', 'POST', {
              confirmed: true,
              phone,
              slot: record.slot,
            }),
          )
          return record
        } catch (e) {
          await recover(true).catch(() => {})
          throw e
        }
      })
    },
    async cancel() {
      return exclusive(async () => {
        if (!record || record.state !== 'prepared') throw new Error('仅可取消未下发的预览。')
        try {
          record = checked(await request('/outgoing/' + id + '/cancel', 'POST'))
          return record
        } catch (e) {
          await recover(true).catch(() => {})
          throw e
        }
      })
    },
    release() {
      if (busy || !record || record.state === 'prepared') throw new Error('请先查看或取消原操作。')
      storage.removeItem(key)
      id = ''
      record = null
      attempted = false
      revision++
    },
  }
}
export const phaseLabels = {
  prepared: '等待确认',
  dispatch_started: '已开始下发',
  waiting_device: '等待板子回报',
  accepted: '板子已接受',
  processed: '板子已处理',
  submitted: '已发送（运营商已接受）',
  unknown: '结果未知',
  failed: '板子报告失败',
  expired: '预览已过期',
  canceled: '预览已取消',
}
export const deliveryLabels = {
  unconfirmed: '送达报告未收到',
  partial: '已收到部分分段送达报告',
  delivering: '运营商继续投递',
  delivered: '运营商报告已送达',
  failed: '送达失败',
  ambiguous: '送达报告无法明确匹配',
}
