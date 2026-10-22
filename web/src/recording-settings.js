export const recordingFields = [
  'voicemail_enabled',
  'voicemail_slots',
  'voicemail_tts',
  'voicemail_amr_url',
  'voicemail_upload_mode',
  'voicemail_proxy_cidrs',
  'voicemail_max_storage_mib',
]

export function recordingMode(values) {
  return values?.voicemail_upload_mode || 'lan'
}

export function recordingEnableProblem(saved, edited) {
  if (
    edited?.voicemail_enabled !== true ||
    recordingMode(edited) !== 'lan' ||
    edited.device_allowlist_enabled !== false
  )
    return ''
  if (saved?.voicemail_enabled === true && recordingMode(saved) === 'lan') return ''
  return '局域网上传需要开启设备来源白名单。请到“系统配置 → 高级连接设置”设置，再重新读取本页。'
}

export function normalizedSettingsValues(values) {
  return {
    ...values,
    backup_max_storage_mib: values.backup_max_storage_mib || 0,
    device_cidrs: values.device_cidrs || [],
    admin_token_header: values.admin_token_header || '',
    device_allowlist_enabled: values.device_allowlist_enabled !== false,
    status_alerts_enabled: values.status_alerts_enabled !== false,
  }
}

// The existing settings API replaces the whole configuration. Each page edits
// its own fields while preserving the other page's saved values and revision.
export function settingsSectionValues(saved, edited, section) {
  const result = { ...saved }
  for (const key of Object.keys(edited)) {
    if (recordingFields.includes(key) === (section === 'recording')) result[key] = edited[key]
  }
  return result
}

export function uploadAddressView(raw, detailed = false) {
  if (!raw) return '未设置'
  try {
    const url = new URL(raw)
    const path =
      detailed && /^\/api\/voicemail\/amr\/[a-f0-9]{32,64}$/.test(url.pathname)
        ? '/api/voicemail/amr/…'
        : '/…'
    return `${url.protocol}//${url.host}${path}`
  } catch {
    return '地址已设置（无法展示）'
  }
}
