import test from 'node:test'
import assert from 'node:assert/strict'
import { createBackupFlow, backupName } from './backup-flow.js'

const id = 'a'.repeat(32)
const file = {
  id,
  name: `sim-backup-20261021T083012Z-${id}.zip`,
  destination: 'browser',
  bytes: 100,
  created_at: 1792571412,
  sha256: 'b'.repeat(64),
}
const list = (items = [], job = null) => ({ items, job, max_storage_bytes: 2048 * 1024 * 1024 })
function fixture(overrides = {}) {
  let state = {}
  const calls = [],
    saves = []
  const flow = createBackupFlow({
    request: async (...args) => {
      calls.push(args)
      return args[1] === 'POST' ? { id, state: 'running' } : list()
    },
    fileRequest: async (...args) => {
      calls.push(args)
      return new Blob(['synthetic'])
    },
    selectFile: async () => null,
    save: (blob, name) => saves.push({ blob, name }),
    changed: (value) => {
      state = value
    },
    makeID: () => id,
    ...overrides,
  })
  return { flow, calls, saves, state: () => state }
}

test('备份文件名含 UTC 时间和唯一标识', () => {
  assert.equal(
    backupName(new Date('2026-10-21T08:30:12.123Z'), id),
    `sim-backup-20261021T083012Z-${id}.zip`,
  )
})
test('生成请求丢失后只读取状态，不重发；发现完整文件后只自动下载一次', async () => {
  let creates = 0
  const f = fixture({
    request: async (path, method) => {
      if (method === 'POST') {
        creates++
        throw new Error('lost response')
      }
      return list([file], { id, state: 'ready' })
    },
  })
  assert.equal(await f.flow.start('browser', true), false)
  assert.equal(f.state().uncertain, true)
  await f.flow.load()
  assert.equal(f.state().uncertain, false)
  assert.equal(f.saves.length, 1)
  await f.flow.load()
  assert.equal(creates, 1)
  assert.equal(f.saves.length, 1)
})
test('重新进入页面可看到任务和文件，不自动下载旧文件；保存服务器模式不触发下载', async () => {
  const saved = { ...file, destination: 'server' }
  const f = fixture({
    request: async (path, method) =>
      method === 'POST' ? { id, state: 'running' } : list([saved], { id, state: 'ready' }),
  })
  await f.flow.load()
  assert.equal(f.saves.length, 0)
  await f.flow.start('server', true)
  await f.flow.load()
  assert.equal(f.saves.length, 0)
  assert.match(f.state().note, /保存到服务器/)
})
test('无确认或保存位置选择被取消时不创建任务', async () => {
  const f = fixture({
    selectFile: async () => {
      throw new DOMException('canceled', 'AbortError')
    },
  })
  assert.equal(await f.flow.start('browser', false), false)
  assert.equal(await f.flow.start('browser', true), false)
  assert.equal(f.calls.length, 0)
  assert.equal(f.state().creating, false)
})
test('下载支持流式写入，取消或离开页面会中止传输且不保存残缺 Blob', async () => {
  let signal, rejectDownload
  const handle = { createWritable: async () => ({}) }
  const f = fixture({
    selectFile: async () => handle,
    fileRequest: (path, method, body, options) => {
      signal = options.signal
      assert.equal(typeof options.openSink, 'function')
      return new Promise((resolve, reject) => {
        rejectDownload = reject
        signal.addEventListener('abort', () => reject(new DOMException('canceled', 'AbortError')))
      })
    },
  })
  const pending = f.flow.download(file)
  await new Promise((resolve) => setImmediate(resolve))
  f.flow.cancelDownload()
  assert.equal(await pending, false)
  assert.equal(signal.aborted, true)
  assert.equal(f.saves.length, 0)
  assert.match(f.state().note, /服务器文件保留/)
  const next = f.flow.download(file)
  await new Promise((resolve) => setImmediate(resolve))
  f.flow.dispose()
  assert.equal(await next, false)
  assert.equal(signal.aborted, true)
  assert.equal(f.saves.length, 0)
  assert.ok(rejectDownload)
})
test('生成任务提交后关闭页面，不再下载或修改界面，也不取消服务器任务', async () => {
  let release
  const f = fixture({
    request: () =>
      new Promise((resolve) => {
        release = resolve
      }),
  })
  const pending = f.flow.start('server', true)
  const before = f.state()
  f.flow.dispose()
  release({ id, state: 'running' })
  assert.equal(await pending, false)
  assert.deepEqual(f.state(), before)
  assert.equal(f.saves.length, 0)
})
test('删除需要单独确认，响应丢失不自动重发，刷新列表后解除不确定状态', async () => {
  let deletions = 0
  const f = fixture({
    request: async (path, method) => {
      if (method === 'POST') {
        deletions++
        throw new Error('lost response')
      }
      return list()
    },
  })
  assert.equal(await f.flow.remove(file, false), false)
  assert.equal(deletions, 0)
  assert.equal(await f.flow.remove(file, true), false)
  assert.equal(f.state().uncertain, true)
  assert.equal(await f.flow.remove(file, true), false)
  await f.flow.load()
  assert.equal(f.state().uncertain, false)
  assert.equal(deletions, 1)
})

test('旧列表响应不会覆盖新任务，异常文件列表不产生下载入口', async () => {
  let resolveOld,
    reads = 0
  const f = fixture({
    request: async (path, method) => {
      if (method === 'POST') return { id, state: 'running' }
      reads++
      if (reads === 1)
        return new Promise((resolve) => {
          resolveOld = resolve
        })
      return list([], { id, state: 'running' })
    },
  })
  const pending = f.flow.load()
  await f.flow.start('server', true)
  resolveOld(list())
  await pending
  assert.equal(f.state().data.job.state, 'running')
  const bad = fixture({ request: async () => list([{ ...file, id: '../sim.db' }]) })
  assert.equal(await bad.flow.load(), false)
  assert.equal(bad.state().data, null)
  assert.equal(await bad.flow.download({ ...file, id: '../sim.db' }), false)
  assert.equal(bad.saves.length, 0)
})
