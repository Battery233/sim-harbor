const consentKeys = ['source_stopped', 'history_complete', 'unknown_acknowledged', 'backup_saved']
function consent(value) {
  if (!consentKeys.every((key) => value?.[key] === true))
    throw new Error('请逐项核对迁移条件，确认后才能继续。')
  return Object.fromEntries(consentKeys.map((key) => [key, true]))
}
function checked(value) {
  if (
    !value ||
    typeof value.restored !== 'boolean' ||
    typeof value.blocked !== 'boolean' ||
    !Number.isSafeInteger(value.confirmed_at) ||
    value.confirmed_at < 0 ||
    !Number.isSafeInteger(value.unknown_outgoing) ||
    value.unknown_outgoing < 0 ||
    (value.restored && !/^[a-f0-9]{32}$/.test(value.revision)) ||
    (value.blocked && (!value.restored || value.confirmed_at !== 0))
  )
    throw new Error('迁移状态不完整，请重新读取。')
  return value
}
// No automatic POST retries. An uncertain confirmation permits only GET until
// an operator reads the persisted result and explicitly prepares a new preview.
export function createMigrationFlow({ request, changed = () => {} }) {
  let state = { status: null, preview: null, busy: false, uncertain: false, error: '' }
  let stopped = false
  function publish() {
    if (!stopped) changed({ ...state })
  }
  async function run(action, mutation = false) {
    if (stopped || state.busy) return null
    state = { ...state, busy: true, error: '' }
    publish()
    try {
      return await action()
    } catch (error) {
      state.error = error.message
      if (mutation) {
        state.uncertain = true
        state.preview = null
      }
      return null
    } finally {
      state.busy = false
      publish()
    }
  }
  return {
    load: () =>
      run(async () => {
        state.preview = null
        state.status = checked(await request('/migration'))
        state.uncertain = false
        return state.status
      }),
    preview: (value) =>
      run(async () => {
        if (state.uncertain) throw new Error('上次结果未确认，请先重新读取迁移状态。')
        state.preview = null
        const data = await request('/migration/preview', 'POST', consent(value))
        checked(data.migration)
        if (
          !data.migration.blocked ||
          !/^[a-f0-9]{24}$/.test(data.token) ||
          !/^[a-f0-9]{64}$/.test(data.backup_sha256) ||
          !Number.isSafeInteger(data.expires_at) ||
          data.expires_at <= Date.now() / 1000
        )
          throw new Error('迁移预览不完整或已过期，请重新读取状态。')
        state.status = data.migration
        state.preview = data
        return data
      }),
    confirm: (value, confirmed) =>
      run(async () => {
        if (
          state.uncertain ||
          !state.preview ||
          !confirmed ||
          state.preview.expires_at <= Date.now() / 1000
        )
          throw new Error('请重新预览并确认恢复外发。')
        const data = await request('/migration/confirm', 'POST', {
          ...consent(value),
          token: state.preview.token,
          confirmed: true,
        })
        const found = checked(data)
        if (found.blocked || !found.restored || found.confirmed_at <= 0)
          throw new Error('解除结果未确认，请读取已保存的迁移状态。')
        state.status = found
        state.preview = null
        return found
      }, true),
    dispose() {
      stopped = true
    },
  }
}
