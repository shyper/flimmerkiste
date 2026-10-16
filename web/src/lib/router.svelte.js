export const route = $state({ path: location.pathname, query: new URLSearchParams(location.search) })

// leaveGuard.check, if set, is asked before navigating away (false = stay).
export const leaveGuard = { check: null }

export function navigate(to, { replace = false } = {}) {
  const url = new URL(to, location.origin)
  if (leaveGuard.check && !leaveGuard.check(url.pathname)) return
  if (url.pathname + url.search === location.pathname + location.search) return
  history[replace ? 'replaceState' : 'pushState']({}, '', url)
  route.path = url.pathname
  route.query = url.searchParams
  window.scrollTo(0, 0)
}

window.addEventListener('popstate', () => {
  route.path = location.pathname
  route.query = new URLSearchParams(location.search)
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
