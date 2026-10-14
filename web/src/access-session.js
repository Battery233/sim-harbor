// Storage may be blocked by privacy settings. Access remains in memory only.
export function createAccessSession(storage = () => sessionStorage) {
  return {
    read() {
      try {
        return storage().getItem('sim-access') || ''
      } catch {
        return ''
      }
    },
    save(value) {
      try {
        storage().setItem('sim-access', value)
        return true
      } catch {
        return false
      }
    },
    clear() {
      try {
        storage().removeItem('sim-access')
      } catch {
        /* Memory access is still cleared. */
      }
    },
  }
}
