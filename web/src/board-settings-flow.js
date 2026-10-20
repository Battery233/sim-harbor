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
    if (channel === 'sms' && row.mode == null && row.tts == null) continue
    if (!Number.isInteger(row.mode) || row.mode < 0 || row.mode > 3 || typeof row.tts !== 'string')
      throw Error('留言查询结果不完整。')
  }
}
