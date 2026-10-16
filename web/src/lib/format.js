import { t, i18n, locale } from './i18n.svelte.js'

export function fmtTime(ms) {
  const opts = i18n.lang === 'de' ? { hour: '2-digit', minute: '2-digit' } : { hour: 'numeric', minute: '2-digit' }
  return new Date(ms).toLocaleTimeString(locale(), opts)
}

export function fmtDay(ms) {
  const d = new Date(ms)
  const today = new Date()
  const tomorrow = new Date(Date.now() + 864e5)
  if (d.toDateString() === today.toDateString()) return t('format.today')
  if (d.toDateString() === tomorrow.toDateString()) return t('format.tomorrow')
  return d.toLocaleDateString(locale(), { weekday: 'short', day: '2-digit', month: '2-digit' })
}

export function fmtDuration(ms) {
  const min = Math.round(ms / 60000)
  if (min < 60) return t('format.minutes', { n: min })
  const h = Math.floor(min / 60)
  const m = min % 60
  if (h >= 48) return t('format.days', { n: Math.round(h / 24) })
  return m ? t('format.hoursMinutes', { h, m }) : t('format.hours', { n: h })
}

export function fmtAgo(unix) {
  if (!unix) return t('format.never')
  const s = Math.round(Date.now() / 1000 - unix)
  if (s < 60) return t('format.justNow')
  if (s < 3600) return t('format.minutesAgo', { n: Math.round(s / 60) })
  if (s < 86400) return t('format.hoursAgo', { n: Math.round(s / 3600) })
  return t('format.daysAgo', { n: Math.round(s / 86400) })
}

// fmtNumber writes a count with thousands separators.
export const fmtNumber = (n) => n.toLocaleString(locale())

export function debounce(fn, ms) {
  let t
  return (...args) => {
    clearTimeout(t)
    t = setTimeout(() => fn(...args), ms)
  }
}

export const kindLabels = {
  get manual() { return t('kind.manual') },
  get smart() { return t('kind.smart') },
  get seasonal() { return t('kind.seasonal') },
  get ai() { return t('kind.ai') },
}

export const kindIcons = {
  manual: 'list',
  smart: 'filter',
  seasonal: 'leaf',
  ai: 'sparkles',
}
