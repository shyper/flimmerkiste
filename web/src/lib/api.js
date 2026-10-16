import { t } from './i18n.svelte.js'
import { app } from './state.svelte.js'

export class ApiError extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

export async function api(method, path, body) {
  const opts = { method, headers: {}, credentials: 'same-origin' }
  if (body instanceof FormData) {
    opts.body = body
  } else if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(path, opts)
  let data = null
  const text = await res.text()
  if (text) {
    try { data = JSON.parse(text) } catch { data = text }
  }
  if (res.status === 401 && !path.startsWith('/api/auth')) {
    app.user = null
  }
  if (!res.ok) {
    throw new ApiError(data?.error || t('api.error', [res.status]), res.status)
  }
  return data
}

export const get = (p) => api('GET', p)
export const post = (p, b) => api('POST', p, b ?? {})
export const put = (p, b) => api('PUT', p, b)
export const del = (p) => api('DELETE', p)
