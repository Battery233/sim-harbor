export function createBoardSettingsFlow({ request }) {
  let busy = false,
    uncertain = false,
    disposed = false
  return {
    async query(action) {
      if (busy || disposed) return null
      if (!['heartbeat_query', 'storage_query', 'voicemail_query'].includes(action))
        throw Error('只支持已列出的查询。')
      busy = true
      try {
        const r = await request('/board/parameters', 'POST', { action })
        if (disposed) return null
        if (action === 'heartbeat_query') {
          if (!Number.isInteger(r.seconds) || r.seconds < 5 || r.seconds > 600)
            throw Error('心跳查询结果不完整。')
          uncertain = false
        } else if (action === 'voicemail_query') {
          checkedVoicemail(r.voicemail)
          uncertain = false
        } else if (
          !r.storage ||
          !['1s', '1t', '2s', '2t'].every((k) => typeof r.storage[k] === 'boolean')
        )
          throw Error('存储查询结果不完整。')
        return r
      } finally {
        busy = false
      }
    },
    async set(seconds, expected, confirmed) {
      if (busy || uncertain || disposed) return null
      if (
        !confirmed ||
        !Number.isInteger(seconds) ||
        seconds < 5 ||
        seconds > 600 ||
        !Number.isInteger(expected)
      )
        throw Error('请先查询并核对要修改的参数。')
      busy = true
      try {
        const r = await request('/board/parameters', 'POST', {
          action: 'heartbeat_set',
          seconds,
          expected,
          confirmed: true,
        })
        if (disposed) return null
        if (!r.verified || r.seconds !== seconds) throw Error('修改结果尚未确认。')
        return r
      } catch (e) {
        if (!disposed) uncertain = true
        throw e
      } finally {
        busy = false
      }
    },
    async applyVoicemail(intent, expected, confirmed, sms) {
      if (busy || uncertain || disposed) return null
      if (!confirmed || !['enable', 'restore'].includes(intent))
        throw Error('请先查询并核对要修改的参数。')
      checkedVoicemail(expected)
      let snapshot = expected
      if (intent === 'enable') {
        busy = true
        try {
          const fresh = await request('/board/parameters', 'POST', { action: 'voicemail_query' })
          if (disposed) return null
          checkedVoicemail(fresh.voicemail)
          snapshot = fresh.voicemail
        } catch (e) {
          if (!disposed) uncertain = true
          throw e
        } finally {
          busy = false
        }
        if (uncertain || disposed) return null
      }
      busy = true
      try {
        const body = {
          action: 'voicemail_apply',
          intent,
          expected: snapshot,
          confirmed: true,
        }
        if (sms?.phone) body.sms_phone = sms.phone
        if (sms?.phones) body.sms_phones = sms.phones
        if (sms?.password) body.sms_password = sms.password
        if (sms?.costConfirmed) body.sms_cost_confirmed = true
        if (intent === 'restore' && sms?.stopAutoAnswer) body.stop_auto_answer_confirmed = true
        const r = await request('/board/parameters', 'POST', body, { timeout: 90000 })
        if (disposed) return null
        checkedVoicemail(r.voicemail)
        if (
          r.verified === true ||
          r.submission === 'submitted' ||
          r.submission === 'accepted' ||
          r.submission === 'verified'
        ) {
          uncertain = false
          return r
        }
        throw Error('修改结果尚未确认。')
      } catch (e) {
        if (!disposed) uncertain = true
        throw e
      } finally {
        busy = false
      }
    },
    dispose() {
      disposed = true
    },
  }
}

export function voicemailSlotEvidence(slot, channel) {
  const names = { 0: '不接听', 1: '接听', 2: '接听并录音', 3: '挂断' }
  const sms = channel === 'sms'
  if (slot?.mode == null && slot?.tts == null)
    return sms
      ? `卡槽 ${slot.slot}：TCP 未返回来电方式 · 提示音预设尚未确认`
      : `卡槽 ${slot.slot}：TCP 未返回来电方式`
  const mode =
    slot.mode == null
      ? sms
        ? '来电方式尚未确认'
        : '来电方式未返回'
      : names[slot.mode] || '来电方式待核对'
  const preset =
    slot.tts == null
      ? sms
        ? '提示音预设尚未确认'
        : '提示音预设未读取'
      : slot.tts
        ? '板子上仍有提示音预设'
        : '提示音预设为空'
  return `卡槽 ${slot.slot}：${mode} · ${preset}`
}

export function voicemailApplyNote(intent, result) {
  if (result?.verified === true) {
    if (result.unchanged) return '板子留言参数已与目标一致，未下发修改。'
    return intent === 'enable' ? '留言参数已下发并读回。' : '已恢复为不接听，并读回确认。'
  }
  const url = result?.url_verified ? '上传地址已用 TCP 读回' : '上传地址尚未确认'
  const fields = (result?.voicemail?.slots || []).map((slot) => {
    const mode = slot.mode == null ? '来电方式尚未确认' : '来电方式已有确认'
    const tts = slot.tts == null ? '提示音预设尚未确认' : '提示音预设已有确认'
    return `卡槽 ${slot.slot}：${mode}，${tts}`
  })
  return `短信已提交，不会自动重发。${url}。${fields.join('；')}。`
}

function checkedVoicemail(value) {
  if (
    !value ||
    typeof value.amr_url !== 'string' ||
    !Array.isArray(value.slots) ||
    value.slots.length !== 2
  )
    throw Error('留言查询结果不完整。')
  const channel = value.channel || 'tcp'
  if (channel !== 'tcp' && channel !== 'sms') throw Error('留言查询结果不完整。')
  for (const slot of [1, 2]) {
    const row = value.slots.find((item) => item.slot === slot)
    if (!row) throw Error('留言查询结果不完整。')
    if (channel === 'sms') {
      if (row.mode != null && (!Number.isInteger(row.mode) || row.mode < 0 || row.mode > 3))
        throw Error('留言查询结果不完整。')
      if (row.tts != null && typeof row.tts !== 'string') throw Error('留言查询结果不完整。')
      continue
    }
    if (!Number.isInteger(row.mode) || row.mode < 0 || row.mode > 3 || typeof row.tts !== 'string')
      throw Error('留言查询结果不完整。')
  }
}
