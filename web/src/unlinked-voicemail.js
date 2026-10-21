const states = ['available', 'missing', 'unavailable']

export function unlinkedAudioPath(item) {
  if (!Number.isSafeInteger(item?.id) || item.id <= 0 || item.file_state !== 'available')
    return null
  return '/voicemails/unlinked/' + item.id + '/audio'
}

function checked(item) {
  if (
    !Number.isSafeInteger(item?.id) ||
    item.id <= 0 ||
    ![0, 1, 2].includes(item.slot) ||
    typeof item.phone !== 'string' ||
    item.phone.length > 128 ||
    !['started_at', 'created_at', 'size'].every(
      (key) => Number.isSafeInteger(item[key]) && item[key] >= 0,
    ) ||
    !states.includes(item.file_state) ||
    typeof item.file_name !== 'string' ||
    (item.file_name !== '' && !/^[a-f0-9]{32}\.amr$/.test(item.file_name)) ||
    (item.file_state === 'available' && (!item.file_name || !item.size || item.size > 2 ** 21))
  )
    throw new Error('录音列表内容不完整，请重新读取。')
  return item
}

export function createUnlinkedVoicemailFlow({ request, changed = () => {} }) {
  let generation = 0,
    stopped = false,
    state = { items: [], next: 0 }
  return {
    async load(more = false) {
      if (stopped || (more && !state.next)) return false
      const version = ++generation
      const before = more ? state.next : 0
      try {
        const result = await request(
          '/voicemails/unlinked' + (before ? '?before=' + before : ''),
          'GET',
        )
        if (stopped || version !== generation) return false
        if (
          !Array.isArray(result?.items) ||
          result.items.length > 50 ||
          !Number.isSafeInteger(result.next_before) ||
          result.next_before < 0
        )
          throw new Error('录音列表响应不完整，请重新读取。')
        const records = result.items.map(checked)
        if (
          records.some((item, index) =>
            index ? item.id >= records[index - 1].id : before && item.id >= before,
          ) ||
          (result.next_before &&
            (records.length !== 50 || result.next_before !== records.at(-1).id))
        )
          throw new Error('录音分页不完整，请重新读取。')
        state = { items: more ? [...state.items, ...records] : records, next: result.next_before }
        changed({ ...state })
        return true
      } catch (error) {
        if (stopped || version !== generation) return false
        throw error
      }
    },
    dispose() {
      stopped = true
      generation++
    },
  }
}
