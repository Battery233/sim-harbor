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
      ? `卡槽 ${slot.slot}：接听方式和板子提示语尚未确认`
      : `卡槽 ${slot.slot}：未读到接听方式和板子提示语`
  const mode =
    slot.mode == null
      ? sms
        ? '接听方式尚未确认'
        : '接听方式未读到'
      : names[slot.mode] || '接听方式待核对'
  const preset =
    slot.tts == null
      ? sms
        ? '板子提示语尚未确认'
        : '板子提示语未读到'
      : slot.tts
        ? '板子上有已保存的提示语'
        : '板子未保存提示语'
  return `卡槽 ${slot.slot}：${mode} · ${preset}`
}

export const voicemailSetupSteps = [
  '首次开启：先在“录音配置”填写应用接收设置，保存并重启应用。',
  '读取板子设置，再选择开启或停止自动接听。读取不会发短信。',
  '核对修改内容和费用后确认执行。结果未确认时，先重新读取，不要重复操作。',
]

export function voicemailPlan(intent, observed, running) {
  const names = { 0: '不接听', 1: '接听', 2: '接听并录音', 3: '挂断' }
  const channel = observed?.channel === 'sms' ? 'sms' : 'tcp'
  const configured = Array.isArray(running?.slots)
    ? running.slots.filter((slot) => slot === 1 || slot === 2)
    : []
  const cloud = running?.environment === 'cloud'
  const held = []
  if (!running) held.push('尚未读到应用设置，请重新读取。本次不会执行修改。')
  if (intent === 'enable' && running && running.enabled !== true)
    held.push('应用留言服务尚未开启。请先完成上方应用接收设置，保存并重启。本次不会执行修改。')
  // Match voicemailTarget: TCP restore can recover the observed board even
  // after the application has been disabled and its configured slots cleared.
  const inferRestoreSlots = intent === 'restore' && channel === 'tcp' && configured.length === 0
  const touched = inferRestoreSlots
    ? (observed?.slots || [])
        .filter((slot) => slot.mode !== 0 || slot.tts !== '')
        .map((slot) => slot.slot)
    : configured
  if (configured.length === 0 && running && !inferRestoreSlots)
    held.push('应用尚未选定留言卡槽。本次不会执行修改。')
  if (intent === 'enable' && running && running.url_configured === false)
    held.push('应用尚未设置接收录音的局域网地址。本次不会执行修改。')
  if (cloud && intent === 'enable') held.push('云端暂不支持接收录音，不能开启。')
  const blocked = held.concat(
    cloud && intent === 'restore' ? ['云端暂不支持接收录音，仍可停止板子自动接听。'] : [],
  )
  const willWrite = held.length === 0
  let smsCount = 0
  let clearsTTS = false
  let clearsURL = false
  const lines = []
  if (willWrite && inferRestoreSlots)
    lines.push('应用未选定留言卡槽，将按刚才读到的板子设置确定停用范围。')
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
      `卡槽 ${number}：${currentMode} → ${names[target]}。${
        !willWrite
          ? '本次不会修改。'
          : channel === 'sms'
            ? modeNeeds
              ? '需发送 1 条设置短信。'
              : '已是目标状态，不重复发送。'
            : modeNeeds
              ? '通过设备连接修改，不发短信。'
              : '已是目标状态，无需修改。'
      }`,
    )
    if (intent === 'restore') {
      const ttsNeeds = slot.tts !== ''
      if (willWrite && ttsNeeds) clearsTTS = true
      if (willWrite && channel === 'sms' && ttsNeeds) smsCount += 1
      const ttsLine =
        slot.tts == null
          ? '未读到，将尝试清除'
          : slot.tts
            ? '有已保存的内容，将清除'
            : '已为空，无需修改'
      lines.push(
        !willWrite
          ? `卡槽 ${number}：本次不修改板子提示语。`
          : `卡槽 ${number}：板子提示语${ttsLine}。`,
      )
    }
  }
  if (intent === 'enable' && willWrite)
    lines.push('提示语：保留板子原有提示语，应用会在接通后播放一次上方配置的提示语。')
  if (intent === 'restore') {
    clearsURL = willWrite && (observed?.amr_url || '') !== ''
    lines.push(
      !willWrite
        ? '本次不修改上传地址。'
        : clearsURL
          ? '上传地址：清空，通过设备连接修改，不发短信。'
          : '上传地址：已为空，无需修改。',
    )
  } else if (!willWrite) {
    lines.push('本次不修改上传地址。')
  } else if (running?.url_matches === true) {
    lines.push('上传地址：与应用设置一致，无需修改。')
  } else if (running) {
    lines.push('上传地址：改为应用设置中的局域网地址，不发短信。')
  }
  const summary = blocked.concat(lines)
  return {
    intent,
    ready: willWrite && (intent === 'restore' || touched.length > 0),
    smsCount,
    sendsSMS: smsCount > 0,
    clearsTTS,
    clearsURL,
    slots: touched,
    cloud,
    text: summary.join('\n'),
  }
}

export function voicemailApplyNote(intent, result) {
  if (result?.verified === true) {
    if (result.unchanged) return '板子设置已符合本次选择，无需修改。'
    return intent === 'enable'
      ? '已确认：目标卡槽已开启自动接听和录音，上传地址已核对。'
      : '已确认：目标卡槽为不接听，板子提示语和上传地址已清除。'
  }
  const url = result?.url_verified ? '上传地址已确认' : '上传地址待确认'
  const fields = (result?.voicemail?.slots || []).map((slot) => {
    const mode = slot.mode == null ? '接听方式待确认' : '接听方式已确认'
    const tts = slot.tts == null ? '板子提示语待确认' : '板子提示语已确认'
    return `卡槽 ${slot.slot}：${mode}，${tts}`
  })
  let lead = '设置短信已提交，尚未确认生效，不会自动重发。'
  if (result?.submission === 'accepted') lead = '板子已接受设置短信，尚未确认生效，不会自动重发。'
  if (result?.submission === 'verified') lead = '设置短信已确认，其他设置仍待核对，不会自动重发。'
  // A false flag means nothing new was sent. Confirmed or accepted evidence stays visible.
  if (
    result?.sms_submitted === false &&
    result?.submission !== 'accepted' &&
    result?.submission !== 'verified'
  )
    lead = '未重复发送之前已提交的设置短信，不会自动重发。'
  if (intent === 'restore') lead = `尚未确认停止自动接听。${lead}`
  return [lead, `${url}。`, ...fields.map((field) => `${field}。`)].join('\n')
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
