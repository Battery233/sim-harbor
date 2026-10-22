import { backupErrors } from './backup-errors.js'

export function backupName(now = new Date(), id = 'backup') {
  return `sim-backup-${now
    .toISOString()
    .replace(/[-:]/g, '')
    .replace(/\.\d{3}Z$/, 'Z')}-${id}.zip`
}
export function backupID() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (x) =>
    x.toString(16).padStart(2, '0'),
  ).join('')
}
export async function selectBackupFile(name) {
  if (!window.showSaveFilePicker) return null
  return window.showSaveFilePicker({
    suggestedName: name,
    types: [{ description: '数据库备份 ZIP', accept: { 'application/zip': ['.zip'] } }],
  })
}

function validBackupFile(file) {
  return (
    file &&
    /^[a-f0-9]{32}$/.test(file.id) &&
    /^sim-backup-\d{8}T\d{6}Z-[a-f0-9]{32}\.zip$/.test(file.name) &&
    Number.isSafeInteger(file.bytes) &&
    file.bytes > 0 &&
    file.bytes <= 544 * 1024 * 1024 &&
    Number.isSafeInteger(file.created_at) &&
    file.created_at > 0 &&
    /^[a-f0-9]{64}$/.test(file.sha256) &&
    ['browser', 'server'].includes(file.destination)
  )
}

// Only GETs are polled. A lost creation/deletion response never resends a POST.
export function createBackupFlow({
  request,
  fileRequest,
  save,
  selectFile,
  changed,
  makeID = backupID,
}) {
  const state = {
    data: null,
    creating: false,
    downloading: '',
    deleting: '',
    uncertain: false,
    error: '',
    note: '',
  }
  let stopped = false,
    loading = false,
    reload = false,
    readError = false,
    generation = 0,
    watchID = '',
    handle = null,
    controller,
    deletingUnknown = ''
  const update = (values) => {
    if (stopped) return
    Object.assign(state, values)
    changed({ ...state })
  }
  const problem = (e) => backupErrors[e.code] || e.message
  async function download(file, selectedHandle) {
    if (
      stopped ||
      !validBackupFile(file) ||
      state.downloading ||
      state.deleting ||
      state.creating ||
      state.uncertain
    )
      return false
    update({ downloading: file.id, error: '', note: '' })
    try {
      const target = selectedHandle === undefined ? await selectFile(file.name) : selectedHandle
      if (stopped) return false
      controller = new AbortController()
      const blob = await fileRequest(`/backups/${file.id}/download`, 'GET', undefined, {
        signal: controller.signal,
        timeout: 10 * 60 * 1000,
        openSink: target ? () => target.createWritable() : undefined,
      })
      if (stopped) return false
      if (blob) save(blob, file.name)
      update({
        note: target ? '备份已写入所选文件。' : '备份已交给浏览器下载，请确认文件保存成功。',
      })
      return true
    } catch (e) {
      if (!stopped && e.name !== 'AbortError')
        update({
          error: `下载未完成：${problem(e)} 文件仍保留在服务器，可重新下载，无需重新生成。`,
        })
      else if (!stopped) update({ note: '下载已取消。服务器文件保留，可稍后重新下载。' })
      return false
    } finally {
      controller = null
      update({ downloading: '' })
    }
  }
  async function load() {
    if (stopped) return false
    if (loading) {
      reload = true
      return false
    }
    loading = true
    const revision = generation
    try {
      const data = await request('/backups')
      if (stopped || revision !== generation) return false
      if (
        !Array.isArray(data.items) ||
        data.items.length > 20 ||
        !data.items.every(validBackupFile) ||
        !Number.isSafeInteger(data.max_storage_bytes) ||
        (data.job &&
          (!/^[a-f0-9]{32}$/.test(data.job.id) ||
            !['running', 'ready', 'failed'].includes(data.job.state)))
      )
        throw new Error('备份列表无法确认，请重新刷新。')
      const matched = watchID && data.job?.id === watchID
      const file = watchID && data.items.find((item) => item.id === watchID)
      const known = matched || file || deletingUnknown
      update({
        data,
        ...(known ? { uncertain: false } : {}),
        ...(readError || known ? { error: '' } : {}),
      })
      readError = false
      deletingUnknown = ''
      if (file && watchID) {
        watchID = ''
        const target = handle
        handle = null
        if (file.destination === 'browser') await download(file, target)
        else update({ note: '备份已保存到服务器，可随时从下方下载。' })
      } else if (matched && data.job.state === 'failed') {
        watchID = ''
        handle = null
        update({ error: backupErrors[data.job.error] || '备份未完成，请核对列表。' })
      }
      return true
    } catch (e) {
      if (revision === generation) {
        readError = true
        update({ error: problem(e) })
      }
      return false
    } finally {
      loading = false
      if (reload && !stopped) {
        reload = false
        void load()
      }
    }
  }
  return {
    load,
    download,
    async start(destination, confirmed) {
      if (
        stopped ||
        !confirmed ||
        state.creating ||
        state.downloading ||
        state.deleting ||
        state.uncertain ||
        state.data?.job?.state === 'running'
      )
        return false
      update({ creating: true, error: '', note: '' })
      generation++
      const id = makeID()
      try {
        handle = destination === 'browser' ? await selectFile(backupName(new Date(), id)) : null
        if (stopped) return false
        watchID = id
        const job = await request('/backups', 'POST', { confirmed: true, id, destination })
        if (stopped) return false
        if (job.id !== id || !['running', 'ready', 'failed'].includes(job.state))
          throw new Error('备份请求结果未确认，请刷新任务状态。')
        update({ data: { ...state.data, job }, note: '服务器正在生成备份，离开页面不会中止。' })
        return true
      } catch (e) {
        if (e.name === 'AbortError' && !watchID) return false
        if (!watchID) update({ error: problem(e) })
        else if (e.status) {
          watchID = ''
          handle = null
          update({ error: problem(e) })
        } else
          update({
            uncertain: true,
            error: '请求结果未确认，请刷新任务和文件列表。不会自动重新生成。',
          })
        return false
      } finally {
        update({ creating: false })
      }
    },
    async remove(file, confirmed) {
      if (
        stopped ||
        !confirmed ||
        !validBackupFile(file) ||
        state.downloading ||
        state.deleting ||
        state.creating ||
        state.uncertain
      )
        return false
      generation++
      update({ deleting: file.id, error: '', note: '' })
      try {
        const result = await request(`/backups/${file.id}/delete`, 'POST', { confirmed: true })
        if (result.deleted !== true) throw new Error('删除结果未确认。')
        update({ note: '服务器上的这份备份已删除。' })
        await load()
        return true
      } catch (e) {
        if (!e.status) deletingUnknown = file.id
        update({ uncertain: !e.status, error: `${problem(e)} 请刷新列表核对，不会自动重试删除。` })
        return false
      } finally {
        update({ deleting: '' })
      }
    },
    acknowledge() {
      watchID = ''
      handle = null
      deletingUnknown = ''
      update({
        uncertain: false,
        error: '',
        note: '请确认列表中的原任务结果后，再决定是否生成新备份。',
      })
    },
    cancelDownload() {
      controller?.abort()
    },
    dispose() {
      stopped = true
      controller?.abort()
      handle = null
    },
  }
}
