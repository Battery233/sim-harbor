import test from 'node:test'
import assert from 'node:assert/strict'
import { slotView, mergeMessages, messageText, hasFormattedText, wifiName } from './presentation.js'
test('WiFi名称解码保留真实加号、百分号及无法解码的原值', () => {
  assert.equal(wifiName('Lab%26WiFi+Guest'), 'Lab&WiFi+Guest')
  assert.equal(wifiName('%E5%AE%B6%E5%BA%AD%20WiFi'), '家庭 WiFi')
  assert.equal(wifiName('Lab%2526WiFi'), 'Lab%26WiFi')
  assert.equal(wifiName('Lab%WiFi'), 'Lab%WiFi')
  assert.equal(wifiName('%FF'), '%FF')
  assert.equal(wifiName(undefined), '')
})
test('活跃连接不把过期的卡槽状态显示为正常', () => {
  const s = {
    online: true,
    status_received_at: 100,
    stale_seconds: 345,
    reported: { slotInfo: { slot1_sta: 'OK', sim1_dbm: '80' } },
  }
  assert.equal(slotView(s, 1, 200).label, '正常')
  assert.equal(slotView(s, 1, 500).label, '待刷新 · 上次正常')
  assert.equal(slotView(s, 1, 500).signal, '80%')
  s.online = false
  assert.equal(slotView(s, 1, 200).good, false)
  assert.equal(slotView({}, 2, 200).label, '尚未查询')
  s.online = true
  s.status_current_session = false
  assert.equal(slotView(s, 1, 200).label, '正在核对')
  assert.equal(slotView(s, 1, 200).good, false)
})
test('刷新保留已加载历史并更新已读及完整状态', () => {
  const merged = mergeMessages(
    [{ id: 2, read: false }, { id: 1 }],
    [{ id: 3 }, { id: 2, read: true }],
  )
  assert.deepEqual(
    merged.map((m) => m.id),
    [3, 2, 1],
  )
  assert.equal(merged[1].read, true)
})

test('短信显示和复制选择服务器排版或原文，不修改正文', () => {
  const m = { body: String.raw`测试\n\n下一段`, display_body: '测试\n\n下一段' }
  const before = structuredClone(m)
  assert.equal(messageText(m), m.display_body)
  assert.equal(messageText(m, true), m.body)
  assert.equal(hasFormattedText(m), true)
  assert.deepEqual(m, before)
})

test('旧接口与异常排版字段安全回退，不猜测或二次解码', () => {
  const raw = String.raw`C:\new\test <script>alert(1)</script> \\n \u0041`
  assert.equal(messageText({ body: raw }), raw)
  assert.equal(messageText({ body: raw, display_body: {} }), raw)
  assert.equal(hasFormattedText({ body: raw }), false)
  assert.equal(hasFormattedText({ body: '一样', display_body: '一样' }), false)
  assert.equal(messageText({ body: '非空', display_body: '' }), '')
  assert.equal(messageText({}), '')
  assert.equal(messageText(null), '')
})

function networkState(info) {
  return {
    online: true,
    status_current_session: true,
    status_received_at: 100,
    stale_seconds: 345,
    reported: { slotInfo: { slot1_sta: 'OK', ...info } },
  }
}

test('主页显示保存号码原文，不补国家码；未插卡不显示旧号码', () => {
  const s = networkState({ sim1_msIsdn: '  SYNTHETIC-NUMBER  ' })
  assert.equal(slotView(s, 1, 200).number, 'SYNTHETIC-NUMBER')
  assert.equal(slotView(s, 1, 500).number, 'SYNTHETIC-NUMBER')
  s.reported.slotInfo.slot1_sta = 'NOSIM'
  assert.equal(slotView(s, 1, 200).number, '')
  s.reported.slotInfo = { slot1_sta: 'OK', sim1_msIsdn: { invalid: true } }
  assert.equal(slotView(s, 1, 200).number, '')
})

test('板子备注与注册网络分别展示；跨境注册不改变卡名', () => {
  const s = networkState({
    sim1_scName: '  境外测试卡  ',
    sim1_plmn: '46001',
    sim1_imsi: '454001234567890',
  })
  const view = slotView(s, 1, 200)
  assert.equal(view.name, '境外测试卡')
  assert.equal(view.operator, '中国联通')
  assert.equal(view.operatorCode, '46001')
  assert.equal(view.crossBorderRoaming, true)
  // Old or disconnected session data must not claim current roaming.
  assert.equal(slotView(s, 1, 500).crossBorderRoaming, false)
  s.status_current_session = false
  assert.equal(slotView(s, 1, 200).crossBorderRoaming, false)
  s.status_current_session = true
  s.reported.slotInfo.slot1_sta = 'NS'
  assert.equal(slotView(s, 1, 200).crossBorderRoaming, false)
})

test('未知网络保留代码；同地区不同网络不推断跨境漫游', () => {
  const s = networkState({ sim1_plmn: '99901', sim1_imsi: '999021234567890' })
  const view = slotView(s, 1, 200)
  assert.equal(view.name, '卡槽 1')
  assert.equal(view.operator, '99901')
  assert.equal(view.operatorCode, '99901')
  assert.equal(view.crossBorderRoaming, false)
  s.reported.slotInfo.sim1_plmn = '46011'
  assert.equal(slotView(s, 1, 200).operator, '中国电信')
  s.reported.slotInfo.sim1_plmn = '46007'
  assert.equal(slotView(s, 1, 200).operator, '中国移动')
  s.reported.slotInfo.sim1_plmn = '__proto__'
  assert.equal(slotView(s, 1, 200).operator, '__proto__')
})

test('缺失或异常卡字段不会产生误导性名称及跨境漫游判断', () => {
  const view = slotView(networkState({ sim1_scName: { private: 'ignored' } }), 1, 200)
  assert.equal(view.name, '卡槽 1')
  assert.equal(view.operator, '—')
  assert.equal(view.crossBorderRoaming, false)
  for (const info of [
    { sim1_plmn: '0', sim1_imsi: '454001234567890' },
    { sim1_plmn: '00000', sim1_imsi: '454001234567890' },
    { sim1_plmn: '46001', sim1_imsi: '000001234567890' },
    { sim1_plmn: '46001', sim1_imsi: '454' },
    { sim1_plmn: '46001', sim1_imsi: '45400notanumber' },
  ]) {
    assert.equal(slotView(networkState(info), 1, 200).crossBorderRoaming, false)
  }
})

test('离线、未来快照与未核对事件不能被心跳伪装为实时正常', () => {
  const s = networkState({ sim1_dbm: '72' })
  assert.equal(slotView(s, 1, 99).good, false)
  assert.equal(slotView(s, 1, 99).label, '待刷新 · 上次正常')
  s.online = false
  assert.equal(slotView(s, 1, 200).label, '离线 · 上次正常')
  assert.equal(slotView(s, 1, 200).signal, '72%')
  s.online = true
  s.status_current_session = false
  assert.equal(slotView(s, 1, 200).label, '正在核对')
  assert.equal(slotView(s, 1, 200).good, false)
  s.status_current_session = true
  s.status_received_at = 200
  assert.equal(slotView(s, 1, 201).label, '正常')
})
