import test from 'node:test'
import assert from 'node:assert/strict'
import { checkedRecordingStorage, recordingStorageWarning } from './recording-storage.js'

const stats = {
  files: 1,
  bytes: 10,
  limit_bytes: 512 << 20,
  limited: true,
  complete: true,
  index_count: 1,
  deleted_count: 0,
  index_limit: 10000,
  file_limit: 10000,
}
test('空间、文件数及累计记录分别提示；局域网不套用公网上限', () => {
  assert.equal(recordingStorageWarning(checkedRecordingStorage(stats)), '')
  assert.match(recordingStorageWarning({ ...stats, bytes: 512 << 20 }), /存储空间已达/)
  assert.match(
    recordingStorageWarning({ ...stats, index_count: 10000, deleted_count: 9999 }),
    /累计录音接收记录已达/,
  )
  assert.match(recordingStorageWarning({ ...stats, files: 9000 }), /目录文件数量接近/)
  assert.equal(recordingStorageWarning({ ...stats, limited: false, index_count: 10000 }), '')
  assert.match(recordingStorageWarning({ ...stats, complete: false }), /仅显示已确认/)
  assert.throws(() => checkedRecordingStorage({ ...stats, deleted_count: 2 }))
  assert.throws(() => checkedRecordingStorage({ ...stats, bytes: -1 }))
})
