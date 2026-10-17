import test from 'node:test'
import assert from 'node:assert/strict'
import { cardFilterOptions, messageSource, cardLocation } from './inbox-cards.js'
import { messageExportQuery } from './download.js'
const a = { id: '0123456789abcdef', name: '同名', tail: '0000', current_slot: 1 }
const b = { id: 'fedcba9876543210', name: '同名', tail: '0000', current_slot: 2 }
const c = { id: '1122334455667788', name: '旧卡', current_slot: 0 }
function state() {
  return {
    online: true,
    status_current_session: true,
    card_locations_verified: true,
    status_received_at: 100,
    stale_seconds: 345,
    cards: [a, b, c],
  }
}
test('same-name cards and more than two identities stay distinct after swapping', () => {
  const s = state(),
    before = cardFilterOptions(s, 101)
  assert.equal(before.length, 5)
  assert.notEqual(before[1].label, before[2].label)
  assert.match(before[3].label, /历史卡/)
  s.cards = [{ ...a, current_slot: 2 }, { ...b, current_slot: 1 }, c]
  const after = cardFilterOptions(s, 102)
  assert.equal(after[1].value, b.id)
  assert.match(after.find((x) => x.value === a.id).label, /当前卡槽 2/)
  assert.equal(new URLSearchParams(messageExportQuery({ card: a.id })).get('card'), a.id)
})

test('保存号码优先于SIM编号尾号，同号码不同身份不合并', () => {
  const s = state()
  s.cards = [{ ...a, number: 'SYNTHETIC-NUMBER' }, { ...b, number: 'SYNTHETIC-NUMBER' }, c]
  const options = cardFilterOptions(s, 101)
  assert(options[1].label.includes('SYNTHETIC-NUMBER'))
  assert(!options[1].label.includes('尾号'))
  assert.notEqual(options[1].label, options[2].label)
  assert.equal(options[1].value, a.id)
  assert.equal(cardLocation(s, c, 101), '历史卡')
  assert.equal(cardLocation(s, c, 500), '位置待确认')
  s.cards = [{ ...a, number: '', tail: '123F' }]
  assert(cardFilterOptions(s, 101)[1].label.includes('SIM编号尾号 123F'))
})
test('stale or old-session location cannot appear current or archived', () => {
  for (const s of [
    { ...state(), online: false },
    { ...state(), status_current_session: false },
    { ...state(), card_locations_verified: false },
    state(),
  ]) {
    const options = cardFilterOptions(s, 500)
    assert.ok(
      options
        .filter((x) => x.value && x.value !== 'unknown')
        .every((x) => x.label.includes('位置待确认')),
    )
  }
})
test('receipt label preserves snapshot regardless of rename or absence', () => {
  assert.equal(messageSource({ card_id: a.id, card_name: '收件时名称', slot: 2 }), '收件时名称')
  assert.equal(messageSource({ card_id: a.id }), '未记录收件时卡名')
  assert.equal(messageSource({ slot: 1 }), '来源未确认')
  assert.equal(messageSource({ card_name: '旧名称' }), '旧名称')
})
test('缺少历史名称时只按固定卡ID补用保存名称，不改变快照或借用当前卡槽', () => {
  const cards = [
    { ...a, name: '卡A新名称', current_slot: 2 },
    { ...b, name: '卡B', current_slot: 1 },
  ]
  const message = { card_id: a.id, card_name: '', slot: 1 }
  const before = structuredClone(message)
  assert.equal(messageSource(message, cards), '卡A新名称 · 自动识别')
  assert.deepEqual(message, before)
  assert.equal(messageSource({ ...message, card_name: '收件时旧名称' }, cards), '收件时旧名称')
  assert.equal(messageSource({ slot: 1 }, cards), '来源未确认')
  assert.equal(messageSource({ card_id: c.id, slot: 1 }, cards), '未记录收件时卡名')
  cards[0].name = ''
  assert.equal(messageSource(message, cards), '未记录收件时卡名')
})
test('export selects identity and rejects mixed slot or malformed IDs', () => {
  assert.equal(new URLSearchParams(messageExportQuery({ card: 'unknown' })).get('card'), 'unknown')
  for (const f of [
    { card: 'unknown', slot: '1' },
    { card: 'invalid' },
    { card: 'ABCDEF0123456789' },
  ])
    assert.throws(() => messageExportQuery(f))
  assert.equal(cardFilterOptions(null, 100)[0].value, '')
})
