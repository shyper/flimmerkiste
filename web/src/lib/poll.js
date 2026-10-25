// poll calls fn right away and again delay ms after each call has finished
// (delay may be a function, asked every round), until stop() is called. stop
// also works while fn is still running, e.g. when a page is left during its
// first load: nothing runs after it. Errors of fn end no round; fn shows them.
export function poll(fn, delay) {
  let timer
  let stopped = false
  let running = false
  const run = async () => {
    clearTimeout(timer)
    if (stopped || running) return
    running = true
    try {
      await fn()
    } catch {
      // fn reports its own errors; the next round tries again
    } finally {
      running = false
    }
    if (!stopped) timer = setTimeout(run, typeof delay === 'function' ? delay() : delay)
  }
  run()
  return {
    // now runs the next round at once (after a change the page waits for)
    now: run,
    stop() {
      stopped = true
      clearTimeout(timer)
    },
  }
}

// alive tells whether a component is still mounted: call it in the script,
// then check alive.yes after every await before touching shared state.
export function alive(onDestroy) {
  const state = { yes: true }
  onDestroy(() => (state.yes = false))
  return state
}
