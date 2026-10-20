import test from 'node:test'
import assert from 'node:assert/strict'
import {
  createBoardSettingsFlow,
  voicemailApplyNote,
  voicemailPlan,
  voicemailSetupSteps,
  voicemailSlotEvidence,
} from './board-settings-flow.js'
test('板子设置没有自动查询，写入须确认且请求只一次，未知后只读确认', async () => {
  const calls = []
  const flow = createBoardSettingsFlow({
    request: async (path, method, body) => {
      calls.push(body)
      if (body.action === 'heartbeat_set') throw Error('lost')
      return { seconds: 120 }
    },
  })
  assert.equal(calls.length, 0)
  await assert.rejects(flow.set(120, 110, false))
  assert.equal(calls.length, 0)
  await assert.rejects(flow.set(120, 110, true))
  await flow.set(120, 110, true)
  assert.equal(calls.length, 1)
  await flow.query('heartbeat_query')
  assert.equal(calls[1].action, 'heartbeat_query')
  assert(!calls[1].confirmed)
})
test('留言下发必须确认，查询不携带写入字段', async () => {
  const calls = []
  const flow = createBoardSettingsFlow({
    request: async (path, method, body) => {
      calls.push(body)
      return {
        voicemail: {
          amr_url: '',
          slots: [
            { slot: 1, mode: 0, tts: '' },
            { slot: 2, mode: 0, tts: '' },
          ],
        },
        verified: true,
      }
    },
  })
  await assert.rejects(flow.applyVoicemail('enable', { amr_url: '', slots: [] }, false))
  assert.equal(calls.length, 0)
  await flow.query('voicemail_query')
  assert.equal(calls[0].action, 'voicemail_query')
  assert.equal(calls[0].confirmed, undefined)
  const expected = {
    amr_url: '',
    slots: [
      { slot: 1, mode: 0, tts: '' },
      { slot: 2, mode: 0, tts: '' },
    ],
  }
  const saved = await flow.applyVoicemail('restore', expected, true)
  assert.equal(saved.verified, true)
  assert.equal(calls[1].action, 'voicemail_apply')
  assert.equal(calls[1].intent, 'restore')
  assert.equal(calls[1].confirmed, true)
})
test('固件拒绝 TCP 时，确认请求才携带短信号码和密码', async () => {
  const calls = []
  const smsState = {
    channel: 'sms',
    amr_url: '',
    slots: [
      { slot: 1, mode: null, tts: null },
      { slot: 2, mode: null, tts: null },
    ],
  }
  const flow = createBoardSettingsFlow({
    request: async (path, method, body, options) => {
      calls.push({ body, options })
      return { voicemail: smsState, verified: true }
    },
  })
  await flow.query('voicemail_query')
  assert.equal(calls[0].body.sms_phone, undefined)
  assert.equal(calls[0].body.sms_password, undefined)
  const restored = {
    channel: 'sms',
    amr_url: '',
    slots: [
      { slot: 1, mode: 0, tts: '' },
      { slot: 2, mode: 0, tts: '' },
    ],
  }
  await flow.applyVoicemail('enable', restored, true, { phone: '13800138000', password: '000000' })
  assert.equal(calls[1].body.action, 'voicemail_query')
  assert.equal(calls[1].body.sms_phone, undefined)
  assert.equal(calls[2].body.action, 'voicemail_apply')
  assert.equal(calls[2].body.intent, 'enable')
  assert.equal(calls[2].body.expected.slots[1].mode, null)
  assert.equal(calls[2].body.expected.slots[1].tts, null)
  assert.equal(calls[2].body.sms_phone, '13800138000')
  assert.equal(calls[2].body.sms_password, '000000')
  assert.equal(calls[2].options.timeout, 90000)
})
test('短信结果未知时返回待确认，并且只提交一次', async () => {
  const calls = []
  const smsState = {
    channel: 'sms',
    amr_url: 'http://192.168.31.10:18880/api/voicemail/amr/abcd',
    slots: [
      { slot: 1, mode: null, tts: null },
      { slot: 2, mode: null, tts: null },
    ],
  }
  const flow = createBoardSettingsFlow({
    request: async (path, method, body) => {
      calls.push(body)
      if (body.action === 'voicemail_query') return { voicemail: smsState }
      return {
        voicemail: smsState,
        verified: false,
        url_verified: true,
        submission: 'accepted',
        sms_submitted: true,
      }
    },
  })
  const saved = await flow.applyVoicemail('enable', smsState, true, {
    phone: '13800138000',
    password: '000000',
    costConfirmed: true,
  })
  assert.equal(saved.verified, false)
  assert.equal(saved.submission, 'accepted')
  assert.equal(calls.filter((item) => item.action === 'voicemail_apply').length, 1)
  assert.equal(calls.at(-1).sms_cost_confirmed, true)
  const restored = await flow.applyVoicemail('restore', smsState, true, {
    phones: { 1: '13800138001', 2: '13800138002' },
    password: '000000',
    costConfirmed: true,
    stopAutoAnswer: true,
  })
  assert.equal(restored.verified, false)
  assert.equal(calls.at(-1).stop_auto_answer_confirmed, true)
  assert.deepEqual(calls.at(-1).sms_phones, { 1: '13800138001', 2: '13800138002' })
  assert.equal(calls.filter((item) => item.action === 'voicemail_apply').length, 2)
})
test('短信通道分别接受来电方式和提示音，TCP 仍要求完整读回', async () => {
  const base = { channel: 'sms', amr_url: '' }
  const slots = (left, right) => ({ ...base, slots: [left, right] })
  const known = [
    slots({ slot: 1, mode: 2, tts: null }, { slot: 2, mode: null, tts: null }),
    slots({ slot: 1, mode: null, tts: '旧预设' }, { slot: 2, mode: null, tts: null }),
    slots({ slot: 1, mode: null, tts: null }, { slot: 2, mode: null, tts: null }),
    slots({ slot: 1, mode: 0, tts: '' }, { slot: 2, mode: 2, tts: '旧预设' }),
  ]
  for (const voicemail of known) {
    const flow = createBoardSettingsFlow({
      request: async () => ({ voicemail }),
    })
    const saved = await flow.query('voicemail_query')
    assert.deepEqual(saved.voicemail, voicemail)
  }
  const partial = known[0]
  const flow = createBoardSettingsFlow({
    request: async () => ({
      voicemail: partial,
      verified: false,
      url_verified: true,
      submission: 'accepted',
    }),
  })
  const applied = await flow.applyVoicemail('restore', partial, true, {
    phone: '13800138000',
    password: '000000',
    costConfirmed: true,
    stopAutoAnswer: true,
  })
  assert.equal(applied.voicemail.slots[0].mode, 2)
  assert.equal(applied.voicemail.slots[0].tts, null)
  assert.equal(
    voicemailSlotEvidence(partial.slots[0], 'sms'),
    '卡槽 1：接听并录音 · 提示音预设尚未确认',
  )
  assert.equal(
    voicemailSlotEvidence(known[1].slots[0], 'sms'),
    '卡槽 1：来电方式尚未确认 · 板子上仍有提示音预设',
  )
  assert.match(voicemailApplyNote('restore', applied), /尚未确认停止自动接听/)
  assert.match(voicemailApplyNote('restore', applied), /卡槽 1：来电方式已确认，提示音仍待核对/)
  assert.match(voicemailApplyNote('restore', applied), /不会自动重发/)
  assert.match(
    voicemailApplyNote('restore', {
      ...applied,
      submission: 'submitted',
      sms_submitted: false,
      url_verified: true,
    }),
    /没有重发已经记录的控制短信，不会自动重发/,
  )
  assert.match(
    voicemailApplyNote('restore', {
      ...applied,
      sms_submitted: true,
      submission: 'submitted',
    }),
    /短信已提交，参数仍待核对，不会自动重发/,
  )
  assert.match(
    voicemailApplyNote('restore', { ...applied, sms_submitted: true }),
    /板子已接受短信，参数仍待核对，不会自动重发/,
  )
  assert.match(
    voicemailApplyNote('restore', {
      ...applied,
      verified: false,
      submission: 'verified',
      sms_submitted: false,
    }),
    /控制短信已确认，整体参数仍待核对/,
  )
  const broken = [
    slots({ slot: 1, mode: '2', tts: null }, { slot: 2, mode: null, tts: null }),
    slots({ slot: 1, mode: 1.5, tts: null }, { slot: 2, mode: null, tts: null }),
    slots({ slot: 1, mode: 4, tts: null }, { slot: 2, mode: null, tts: null }),
    slots({ slot: 1, mode: 2, tts: 1 }, { slot: 2, mode: null, tts: null }),
    slots({ slot: 1, mode: true, tts: null }, { slot: 2, mode: null, tts: null }),
    {
      channel: 'tcp',
      amr_url: '',
      slots: [
        { slot: 1, mode: 2, tts: null },
        { slot: 2, mode: 0, tts: '' },
      ],
    },
    {
      amr_url: '',
      slots: [
        { slot: 1, mode: null, tts: null },
        { slot: 2, mode: 0, tts: '' },
      ],
    },
  ]
  for (const voicemail of broken) {
    const rejected = createBoardSettingsFlow({
      request: async () => ({ voicemail }),
    })
    await assert.rejects(rejected.query('voicemail_query'), /留言查询结果不完整/)
  }
})
test('板子设置结果不完整不声称成功，不支持任意命令', async () => {
  const flow = createBoardSettingsFlow({ request: async () => ({ seconds: 120 }) })
  await assert.rejects(flow.query('restart'))
  await assert.rejects(flow.query('storage_query'))
  await assert.rejects(flow.set(120, 110, true))
  assert.equal(await flow.set(120, 110, true), null)
})
test('启用和恢复预览按卡槽给出短信条数，且不含密码或原始指令', () => {
  assert.equal(voicemailSetupSteps.length, 5)
  const unknown = {
    channel: 'sms',
    amr_url: 'http://192.168.31.10:18880/api/voicemail/amr/abcd',
    slots: [
      { slot: 1, mode: null, tts: null },
      { slot: 2, mode: null, tts: null },
    ],
  }
  const one = voicemailPlan('restore', unknown, { enabled: true, slots: [2], environment: 'local' })
  assert.equal(one.smsCount, 2)
  assert.equal(one.sendsSMS, true)
  assert.equal(one.clearsTTS, true)
  assert.equal(one.clearsURL, true)
  assert.deepEqual(one.slots, [2])
  const both = voicemailPlan('enable', unknown, {
    enabled: true,
    slots: [1, 2],
    environment: 'local',
    url_matches: false,
  })
  assert.equal(both.smsCount, 2)
  assert.equal(both.clearsTTS, false)
  assert.match(both.text, /不清空、也不下发提示音/)
  const tcp = voicemailPlan(
    'enable',
    { ...unknown, channel: 'tcp' },
    {
      enabled: true,
      slots: [2],
      environment: 'local',
      url_matches: true,
    },
  )
  assert.equal(tcp.smsCount, 0)
  assert.match(tcp.text, /不发短信/)
  const idle = voicemailPlan(
    'restore',
    {
      channel: 'sms',
      amr_url: '',
      slots: [
        { slot: 1, mode: 0, tts: '' },
        { slot: 2, mode: 0, tts: '' },
      ],
    },
    { enabled: false, slots: [2], environment: 'cloud' },
  )
  assert.equal(idle.smsCount, 0)
  assert.equal(idle.clearsTTS, false)
  assert.equal(idle.clearsURL, false)
  assert.match(idle.text, /云端不接收录音/)
  const encoded = JSON.stringify(voicemailPlan('restore', unknown, { enabled: true, slots: [2] }))
  assert.doesNotMatch(encoded, /000000|\*#slot|sms_password/)
  const notRunning = voicemailPlan('enable', unknown, {
    enabled: false,
    slots: [2],
    environment: 'local',
    url_configured: true,
  })
  assert.equal(notRunning.smsCount, 0)
  assert.equal(notRunning.ready, false)
  assert.match(notRunning.text, /这次不会下发/)
  assert.doesNotMatch(notRunning.text, /会发送/)
  const noURL = voicemailPlan('enable', unknown, {
    enabled: true,
    slots: [2],
    environment: 'local',
    url_configured: false,
  })
  assert.equal(noURL.smsCount, 0)
  assert.match(noURL.text, /还没有局域网上传地址/)
  assert.match(voicemailPlan('enable', unknown, { enabled: false, slots: [] }).text, /不会下发/)
})

test('关闭应用且未选卡槽时，TCP 恢复预览列出实际恢复范围', () => {
  const observed = {
    channel: 'tcp',
    amr_url: 'http://example.invalid/upload',
    slots: [
      { slot: 1, mode: 2, tts: '合成预设' },
      { slot: 2, mode: 0, tts: '' },
    ],
  }
  const running = { enabled: false, slots: [], environment: 'local', url_configured: false }
  const plan = voicemailPlan('restore', observed, running)
  assert.equal(plan.ready, true)
  assert.deepEqual(plan.slots, [1])
  assert.equal(plan.clearsTTS, true)
  assert.equal(plan.clearsURL, true)
  assert.equal(plan.smsCount, 0)
  assert.match(plan.text, /卡槽 1：当前来电方式接听并录音，目标不接听/)
  assert.match(plan.text, /卡槽 1：提示音仍有内容，会清空/)
  assert.match(plan.text, /上传地址不是空，会清空/)
  assert.doesNotMatch(plan.text, /这次不会下发|卡槽 2：/)

  // Configured slots still restrict recovery; do not expand to the other slot.
  const configured = voicemailPlan('restore', observed, { ...running, slots: [2] })
  assert.deepEqual(configured.slots, [2])
  assert.equal(configured.clearsTTS, false)
  assert.doesNotMatch(configured.text, /卡槽 1：/)

  const cloud = voicemailPlan('restore', observed, { ...running, environment: 'cloud' })
  assert.equal(cloud.ready, true)
  assert.deepEqual(cloud.slots, [1])
  assert.match(cloud.text, /云端不接收录音，仍可恢复板子为不接听/)
})

test('TCP 无卡槽恢复仍可单独清空上传地址，已恢复的卡槽不重复修改', () => {
  const observed = {
    channel: 'tcp',
    amr_url: 'http://example.invalid/upload',
    slots: [
      { slot: 1, mode: 0, tts: '' },
      { slot: 2, mode: 0, tts: '' },
    ],
  }
  const running = { enabled: false, slots: [] }
  const urlOnly = voicemailPlan('restore', observed, running)
  assert.equal(urlOnly.ready, true)
  assert.deepEqual(urlOnly.slots, [])
  assert.equal(urlOnly.clearsURL, true)
  assert.equal(urlOnly.clearsTTS, false)
  assert.equal(urlOnly.smsCount, 0)
  assert.doesNotMatch(urlOnly.text, /这次不会下发|卡槽 [12]：/)

  const unchanged = voicemailPlan('restore', { ...observed, amr_url: '' }, running)
  assert.equal(unchanged.ready, true)
  assert.deepEqual(unchanged.slots, [])
  assert.equal(unchanged.clearsURL, false)
  assert.match(unchanged.text, /上传地址已空，不重复清空/)

  const presetOnly = voicemailPlan(
    'restore',
    { ...observed, slots: [observed.slots[0], { slot: 2, mode: 0, tts: '合成预设' }] },
    running,
  )
  assert.deepEqual(presetOnly.slots, [2])
  assert.equal(presetOnly.clearsTTS, true)
})

test('没有运行配置或短信通道未选卡槽时，恢复预览不可执行', () => {
  const observed = {
    channel: 'sms',
    amr_url: 'http://example.invalid/upload',
    slots: [
      { slot: 1, mode: null, tts: null },
      { slot: 2, mode: null, tts: null },
    ],
  }
  for (const running of [null, { enabled: false, slots: [] }]) {
    const plan = voicemailPlan('restore', observed, running)
    assert.equal(plan.ready, false)
    assert.equal(plan.clearsURL, false)
    assert.equal(plan.clearsTTS, false)
    assert.equal(plan.smsCount, 0)
    assert.deepEqual(plan.slots, [])
    assert.match(plan.text, /这次不会下发上传地址/)
  }
})
