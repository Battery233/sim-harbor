export const recordingFields = [
  'voicemail_enabled',
  'voicemail_slots',
  'voicemail_tts',
  'voicemail_amr_url',
]

export function normalizedSettingsValues(values) {
  return {
    ...values,
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

export function uploadAddressView(raw) {
  if (!raw) return '未设置'
  try {
    const url = new URL(raw)
    return `${url.protocol}//${url.host}/…`
  } catch {
    return '地址已设置（无法展示）'
  }
}
