export const route = $state({ path: location.pathname })

// leaveGuard holds the checks asked before navigating away (false = stay).
// A page holds its own check and releases only that one, so a page mounting
// before the previous one is gone never loses its guard.
export const leaveGuard = {
  checks: [],
  hold(check) {
    this.checks.push(check)
  },
  release(check) {
    this.checks = this.checks.filter((c) => c !== check)
  },
  allows(to) {
    return this.checks.every((c) => c(to))
  },
}

export function navigate(to, { replace = false } = {}) {
  const url = new URL(to, location.origin)
  if (url.origin !== location.origin) {
    location.href = url.href // another site: no in-app navigation
    return
  }
  if (!leaveGuard.allows(url.pathname)) return
  if (url.pathname + url.search === location.pathname + location.search) return
  history[replace ? 'replaceState' : 'pushState']({}, '', url)
  route.path = url.pathname
  window.scrollTo(0, 0)
}

window.addEventListener('popstate', () => {
  route.path = location.pathname
})

// Svelte action: <a href="/x" use:link>
export function link(node) {
  const onClick = (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey) return
    e.preventDefault()
    navigate(node.getAttribute('href'))
  }
  node.addEventListener('click', onClick)
  return { destroy: () => node.removeEventListener('click', onClick) }
}

// match('/channels/:id') -> { id } | null
export function match(pattern, path = route.path) {
  const p = pattern.split('/').filter(Boolean)
  const s = path.split('/').filter(Boolean)
  if (p.length !== s.length) return null
  const params = {}
  for (let i = 0; i < p.length; i++) {
    if (p[i].startsWith(':')) params[p[i].slice(1)] = decodeURIComponent(s[i])
    else if (p[i] !== s[i]) return null
  }
  return params
}
