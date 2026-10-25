import { t } from './i18n.svelte.js'

export const app = $state({
  ready: false,
  user: null,
  status: null,
  meta: null,
  facets: null,
  missing: [], // manual entries of the open channel that no longer exist in Plex ("type|key|season")
  toasts: [],
})

let toastId = 0
export function toast(message, kind = 'info') {
  const id = ++toastId
  app.toasts.push({ id, message, kind })
  setTimeout(() => {
    const i = app.toasts.findIndex((t) => t.id === id)
    if (i >= 0) app.toasts.splice(i, 1)
  }, kind === 'error' ? 6000 : 3500)
}

export const isAdmin = () => app.user?.role === 'admin'

// copyText puts text on the clipboard and says whether it worked.
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    toast(t('tunerHelp.copied'), 'success')
  } catch {
    toast(t('tunerHelp.copyFailed'))
  }
}
