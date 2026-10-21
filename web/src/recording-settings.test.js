import test from 'node:test'
import assert from 'node:assert/strict'
import { createSettingsFlow } from './settings-flow.js'
import {
  settingsSectionValues,
  normalizedSettingsValues,
  uploadAddressView,
} from './recording-settings.js'

const saved = {
  environment: 'local',
  data_directory: '/synthetic/data',
  device_cidrs: ['192.168.31.20/32'],
  outbound_enabled: false,
  log_level: 'info',
  voicemail_enabled: true,
  voicemail_slots: [2],
  voicemail_tts: 'SYNTHETIC-GREETING',
  voicemail_amr_url: 'http://192.168.31.10:18880/api/voicemail/amr/' + 'a'.repeat(32),
}

test('录音页保存沿用旧版完整配置，只改录音字段，不改外发、路径或凭据', async () => {
  const snapshot = {
    values: saved,
    revision: 'b'.repeat(64),
    pending_restart: false,
    credentials_configured: { aes_key: true, aes_iv: true, admin_token: true, bark_key: true },
  }
  const calls = []
  const flow = createSettingsFlow({
    request: async (...args) => {
      calls.push(args)
      return snapshot
    },
  })
  await flow.load()
  const values = settingsSectionValues(
    saved,
    {
      ...saved,
      voicemail_enabled: false,
      outbound_enabled: true,
      data_directory: '/wrong',
    },
    'recording',
  )
  await flow.save(values, {}, snapshot.revision, true)
  assert.deepEqual(calls[1][2].values, { ...saved, voicemail_enabled: false })
  assert.deepEqual(calls[1][2].secrets, {})
  assert.equal(calls[1][2].revision, snapshot.revision)
  assert.equal(saved.voicemail_enabled, true)
  assert.equal(calls.length, 2)
})

test('系统配置保存保留录音设置；旧配置缺少可选字段时不会凭空启用或生成令牌', () => {
  const values = settingsSectionValues(
    saved,
    {
      ...saved,
      log_level: 'warn',
      voicemail_enabled: false,
      voicemail_slots: [],
      voicemail_amr_url: '',
    },
    'system',
  )
  assert.deepEqual(values, { ...saved, log_level: 'warn' })
  const old = { environment: 'cloud', data_directory: '/synthetic/data', voicemail_enabled: false }
  const result = settingsSectionValues(old, normalizedSettingsValues(old), 'recording')
  assert.deepEqual(result, old)
  assert.equal(Object.hasOwn(result, 'voicemail_amr_url'), false)
  assert.deepEqual(
    normalizedSettingsValues({ device_cidrs: null }),
    normalizedSettingsValues({ device_cidrs: [] }),
  )
})

test('地址核对展示不泄露路径令牌、查询参数或 URL 凭据', () => {
  const url = saved.voicemail_amr_url + '?phone=SYNTHETIC-CALLER&start=123'
  assert.equal(uploadAddressView(url), 'http://192.168.31.10:18880/…')
  assert.equal(
    uploadAddressView(
      'smtp://SYNTHETIC-USER:SYNTHETIC-PASSWORD@mail.example.com:587/path?pwd=SECRET',
    ),
    'smtp://mail.example.com:587/…',
  )
  assert.equal(uploadAddressView(''), '未设置')
  assert.equal(uploadAddressView('INVALID-SECRET'), '地址已设置（无法展示）')
})
