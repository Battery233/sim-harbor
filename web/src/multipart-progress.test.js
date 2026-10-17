import test from 'node:test'
import assert from 'node:assert/strict'
import { multipartProgress } from './multipart-progress.js'

test('长短信进度去重计数，按序列出缺片，普通短信不显示进度', () => {
  const progress = multipartProgress({
    multipart: true,
    complete: false,
    parts_total: 3,
    part_indices: [3, 1, 1],
  })
  assert.equal(progress.label, '等待分片 · 2/3')
  assert.match(progress.detail, /缺少第 2 片/)
  assert.equal(multipartProgress({ complete: true }), null)
  assert.equal(multipartProgress(null), null)
})
test('明确单片的短短信不显示进度，即使带组标记或未完整标记', () => {
  for (const complete of [true, false]) {
    assert.equal(
      multipartProgress({ multipart: true, complete, parts_total: 1, part_indices: [1] }),
      null,
    )
  }
})
test('旧记录没有总数时不推算，只有正文时不虚报零片', () => {
  const legacy = multipartProgress({
    multipart: true,
    complete: false,
    parts_total: 0,
    part_indices: [1],
  })
  assert.equal(legacy.label, '等待分片 · 已保存 1 片')
  assert.match(legacy.detail, /总片数未知/)
  const absent = multipartProgress({
    multipart: true,
    complete: false,
    parts_total: 3,
    part_indices: [],
  })
  assert.equal(absent.label, '等待分片 · 共 3 片')
  assert.match(absent.detail, /无法确认已收片数/)
})
test('完整聚合上报显示收齐，异常序号和总数不参与计数', () => {
  const full = multipartProgress({
    multipart: true,
    complete: true,
    parts_total: 3,
    part_indices: [],
  })
  assert.equal(full.label, '已收齐 · 3/3')
  const invalid = multipartProgress({
    multipart: true,
    complete: false,
    parts_total: 999,
    part_indices: [-1, 0, 1, 256, '2'],
  })
  assert.equal(invalid.label, '等待分片 · 已保存 1 片')
  assert.match(multipartProgress({ multipart: true, complete: true }).detail, /旧记录未保存总片数/)
})
test('所有序号都有但完整标记仍为否时，不擅自宣布收齐', () => {
  const progress = multipartProgress({
    multipart: true,
    complete: false,
    parts_total: 2,
    part_indices: [1, 2],
  })
  assert.match(progress.label, /等待分片/)
  assert.match(progress.detail, /完整状态仍待核对/)
})
