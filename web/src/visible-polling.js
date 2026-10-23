// Only read refreshes use this scheduler. Hiding a tab never cancels a transfer
// or a submitted action, and returning never replays a write.
export function startVisiblePolling(run, milliseconds, options = {}) {
  const doc = options.document || globalThis.document
  const schedule = options.setInterval || globalThis.setInterval
  const clear = options.clearInterval || globalThis.clearInterval
  let stopped = false,
    running = false,
    timer
  async function refresh() {
    if (stopped || running || doc?.hidden) return
    running = true
    try {
      await run()
    } finally {
      running = false
    }
  }
  function tick() {
    // Views present their own read errors. A failed read is never retried here.
    void refresh().catch(() => {})
  }
  function visibility() {
    if (timer !== undefined) clear(timer)
    timer = undefined
    if (stopped || doc?.hidden) return
    timer = schedule(tick, milliseconds)
    tick()
  }
  doc?.addEventListener('visibilitychange', visibility)
  if (!doc?.hidden) timer = schedule(tick, milliseconds)
  return () => {
    stopped = true
    if (timer !== undefined) clear(timer)
    doc?.removeEventListener('visibilitychange', visibility)
  }
}

export function needsInboxRefresh(tab, detailOpen = false) {
  return tab === 'inbox' || detailOpen
}
