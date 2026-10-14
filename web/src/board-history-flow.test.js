import test from 'node:test'
import assert from 'node:assert/strict'
import { createBoardHistoryFlow, boardStatusView } from './board-history-flow.js'
test('板子历史不自动查询，只发只读动作，旧范围和退出后结果不展示', async () => {
  let resolve
  const calls = []
  const f = createBoardHistoryFlow({
    request: (path, method, body) => {
      calls.push([path, method, body])
      return new Promise((r) => (resolve = r))
    },
  })
  assert.equal(calls.length, 0)
  const pending = f.load('sms', 1, 1)
  f.invalidate()
  resolve({ kind: 'sms', slot: 1, offset: 1, items: [], has_more: false })
  assert.equal(await pending, null)
  assert.deepEqual(calls[0], ['/board/history', 'POST', { kind: 'sms', slot: 1, offset: 1 }])
  const next = f.load('calls', 2, 11)
  f.dispose()
  resolve({ kind: 'calls', slot: 2, offset: 11, items: [], has_more: false })
  assert.equal(await next, null)
})
test('原始状态默认隐藏身份、凭据始终隐藏，历史错误卡槽或结构拒绝', async () => {
  const raw = {
    slotInfo: { sim1_iccId: 'SYNTHETIC', sim1_imsi: 'SYNTHETIC' },
    wifi: { ssid: 'SYNTHETIC' },
    aes: { key: 'SYNTHETIC-KEY', iv: 'SYNTHETIC-IV' },
  }
  assert.equal(boardStatusView(raw).slotInfo.sim1_iccId, '身份信息已隐藏')
  assert.equal(boardStatusView(raw, true).aes.key, '已隐藏')
  assert.equal(raw.aes.key, 'SYNTHETIC-KEY')
  const f = createBoardHistoryFlow({
    request: async () => ({ kind: 'sms', slot: 2, offset: 1, items: [], has_more: false }),
  })
  await assert.rejects(f.load('sms', 1, 1))
  await assert.rejects(f.load('sms', 0, 1))
})
