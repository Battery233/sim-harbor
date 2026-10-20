export function voicemailServiceStatus({
  savedEnabled,
  runningEnabled,
  pendingRestart,
  environment,
}) {
  const saved = savedEnabled ? '已保存：开启。' : '已保存：关闭。'
  const running =
    runningEnabled === true
      ? '当前生效：开启。'
      : runningEnabled === false
        ? '当前生效：关闭。'
        : '当前生效状态尚未读取。'
  const restart = pendingRestart ? '有配置等待重启应用后生效。' : ''
  const board = '板子是否接听，请到“板子设置”读取确认。'
  const cloud = environment === 'cloud' ? '云端暂不支持接收录音，请保持关闭。' : ''
  return saved + running + restart + board + cloud
}

export function settingsSavedNote(turningOffVoicemail) {
  if (turningOffVoicemail)
    return '已保存。重启应用后，将停止播放提示语和接收新录音。板子仍可能自动接听；请到“板子设置”读取并执行停止操作，确认结果。'
  return '已保存，重启应用后生效。板子设置不会随之改变。'
}

export const settingsErrors = {
  settings_access_required: '系统配置需要已启用的管理凭据。',
  settings_unavailable: '无法读取或保存私有配置，请保留当前设置并查看运行诊断。',
  settings_conflict: '配置已被其他页面修改，请重新读取后核对。',
  settings_confirmation_required: '请先核对配置并勾选确认。',
  voicemail_confirmation_required: '请勾选留言确认，了解自动接听录音可能产生通话费用。',
  invalid_settings:
    '配置未通过校验：请核对地址、白名单、AES长度、管理凭据长度、鉴权请求头名称和数值范围。',
  settings_restart_unavailable: '当前运行入口不支持网页重启。',
  settings_restarting: '应用正在重启，请稍后刷新，不要重复提交。',
}
function validate(data) {
  if (
    !data ||
    !data.values ||
    typeof data.values !== 'object' ||
    Array.isArray(data.values) ||
    Object.hasOwn(data.values, 'credentials') ||
    !/^[a-f0-9]{64}$/.test(data.revision) ||
    typeof data.pending_restart !== 'boolean' ||
    !data.credentials_configured ||
    typeof data.credentials_configured !== 'object'
  )
    throw new Error('系统配置响应不完整，请重新读取。')
  for (const key of ['aes_key', 'aes_iv', 'admin_token', 'bark_key'])
    if (typeof data.credentials_configured[key] !== 'boolean') throw new Error('凭据状态不完整。')
  return data
}
export function createSettingsFlow({ request }) {
  let stopped = false,
    busy = false,
    uncertain = false,
    restartAttempted = false,
    generation = 0
  return {
    async load() {
      const revision = ++generation
      const data = validate(await request('/settings'))
      if (stopped || generation !== revision) return null
      uncertain = false
      if (!data.pending_restart) restartAttempted = false
      return data
    },
    async save(values, secrets, revision, confirmed, voicemailConfirmed = false) {
      if (stopped || busy || uncertain || restartAttempted || !confirmed)
        throw new Error('请先读取配置并确认；结果不确定时不会重复保存。')
      busy = true
      const version = ++generation
      uncertain = true
      try {
        const data = validate(
          await request('/settings', 'PUT', {
            values,
            secrets,
            revision,
            confirmed: true,
            voicemail_confirmed: voicemailConfirmed === true,
          }),
        )
        if (stopped || generation !== version) return null
        uncertain = false
        return data
      } finally {
        busy = false
      }
    },
    async restart(revision, confirmed) {
      if (stopped || busy || restartAttempted || uncertain || !confirmed)
        throw new Error('请先保存并确认；重启请求不会自动重复。')
      busy = true
      restartAttempted = true
      try {
        const data = await request('/settings/restart', 'POST', { revision, confirmed: true })
        if (stopped) return null
        if (data?.restarting !== true)
          throw new Error('重启结果未确认，请刷新页面查看；不会重复请求。')
        return data
      } finally {
        busy = false
      }
    },
    dispose() {
      stopped = true
      generation++
    },
  }
}
