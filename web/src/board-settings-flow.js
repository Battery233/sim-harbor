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

export const voicemailSetupSteps = [
  '系统配置里保存卡槽、提示音和局域网地址。这只是已保存配置。',
  '重启管理应用。重启之后才是运行配置。',
  '到板子设置读取留言参数。读取不会发短信。',
  '核对下面的预览，再确认下发。短信通道还要确认费用；停止自动接听要单独确认。',
  '按证据看结果：已提交、板子已接受、参数已确认、仍待核对，不是同一个状态。',
]

export function voicemailPlan(intent, observed, running) {
  const names = { 0: '不接听', 1: '接听', 2: '接听并录音', 3: '挂断' }
  const channel = observed?.channel === 'sms' ? 'sms' : 'tcp'
  const configured = Array.isArray(running?.slots)
    ? running.slots.filter((slot) => slot === 1 || slot === 2)
    : []
  const cloud = running?.environment === 'cloud'
  const held = []
  if (!running) held.push('还没有运行配置，不能预计短信条数。')
  if (intent === 'enable' && running && running.enabled !== true)
    held.push('运行中的留言服务仍是关闭的。请先在系统配置保存并重启。这不表示板子已经恢复不接听。')
  if (configured.length === 0 && running) held.push('运行配置没有选中卡槽，这次不会下发。')
  if (intent === 'enable' && running && running.url_configured === false)
    held.push('运行配置还没有局域网上传地址，这次不会下发。')
  const blocked = held.concat(cloud ? ['云端不接收录音，不能把这次下发当成录音已经可用。'] : [])
  const willWrite = held.length === 0
  const touched = configured.length > 0 ? configured : []
  let smsCount = 0
  let clearsTTS = false
  let clearsURL = false
  const lines = []
  for (const number of touched) {
    const slot = (observed?.slots || []).find((item) => item.slot === number) || {
      slot: number,
      mode: null,
      tts: null,
    }
    const target = intent === 'enable' ? 2 : 0
    const modeKnown = slot.mode != null
    const modeNeeds = slot.mode !== target
    const currentMode = modeKnown ? names[slot.mode] || '待核对' : '未知'
    if (willWrite && channel === 'sms' && modeNeeds) smsCount += 1
    lines.push(
      `卡槽 ${number}：当前来电方式${currentMode}，目标${names[target]}。${
        !willWrite
          ? '这次不会下发。'
          : channel === 'sms'
            ? modeNeeds
              ? '会发送 1 条来电方式短信。'
              : '来电方式已是目标值，不重复发送。'
            : '来电方式走板子连接，不发短信。'
      }`,
    )
    if (intent === 'restore') {
      const ttsNeeds = slot.tts !== ''
      if (willWrite && ttsNeeds) clearsTTS = true
      if (willWrite && channel === 'sms' && ttsNeeds) smsCount += 1
      const ttsLine =
        slot.tts == null ? '未知，仍会尝试清空' : slot.tts ? '仍有内容，会清空' : '已空，不重复清空'
      lines.push(
        !willWrite ? `卡槽 ${number}：提示音这次不会下发。` : `卡槽 ${number}：提示音${ttsLine}。`,
      )
    } else {
      lines.push(`卡槽 ${number}：这次启用不清空、也不下发提示音。接通后由运行配置播放一次。`)
    }
  }
  if (intent === 'restore') {
    clearsURL = willWrite && (observed?.amr_url || '') !== ''
    lines.push(
      !willWrite
        ? '这次不会下发上传地址。'
        : clearsURL
          ? '上传地址不是空，会清空。这不是短信。'
          : '上传地址已空，不重复清空。',
    )
  } else if (!willWrite) {
    lines.push('这次不会写入上传地址。')
  } else if (running?.url_matches === true) {
    lines.push('上传地址已与运行配置一致，不重复写入。')
  } else if (running) {
    lines.push('上传地址与运行配置不一致或尚未配置，会按运行配置写入局域网地址。这不是短信。')
  }
  const summary = blocked.concat(lines)
  return {
    intent,
    ready: blocked.length === 0 && touched.length > 0,
    smsCount,
    sendsSMS: smsCount > 0,
    clearsTTS,
    clearsURL,
    slots: touched,
    cloud,
    text: summary.join(''),
  }
}

export function voicemailApplyNote(intent, result) {
  if (result?.verified === true) {
    if (result.unchanged) return '板子留言参数已与目标一致，未下发修改。'
    return intent === 'enable'
      ? '板子参数已确认。来电方式和上传地址已读回。'
      : '板子参数已确认。已恢复为不接听，并读回确认。'
  }
  const url = result?.url_verified ? '上传地址已读回' : '上传地址仍待核对'
  const fields = (result?.voicemail?.slots || []).map((slot) => {
    const mode = slot.mode == null ? '来电方式仍待核对' : '来电方式已确认'
    const tts = slot.tts == null ? '提示音仍待核对' : '提示音已确认'
    return `卡槽 ${slot.slot}：${mode}，${tts}`
  })
  let lead = '短信已提交，参数仍待核对，不会自动重发。'
  if (result?.submission === 'accepted') lead = '板子已接受短信，参数仍待核对，不会自动重发。'
  if (result?.submission === 'verified') lead = '控制短信已确认，整体参数仍待核对，不会自动重发。'
  // A false flag means nothing new was sent. Confirmed or accepted evidence stays visible.
  if (
    result?.sms_submitted === false &&
    result?.submission !== 'accepted' &&
    result?.submission !== 'verified'
  )
    lead = '没有重发已经记录的控制短信，不会自动重发。'
  if (intent === 'restore') lead = `尚未确认停止自动接听。${lead}`
  return `${lead}${url}。${fields.join('；')}。`
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
