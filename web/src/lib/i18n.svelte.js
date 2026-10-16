// Texts of the web interface in the installation's language (German or
// English; English is the fallback). t() reads the language reactively, so
// markup follows a change; values computed once in scripts do not, which is
// why the app reloads after the language is changed.
import de from '../locales/de.json'
import en from '../locales/en.json'

const catalogs = { de, en }

// server: name of the linked media server, filled into {server}.
export const i18n = $state({ lang: 'en', server: 'Plex' })

export function setLang(lang) {
  i18n.lang = catalogs[lang] ? lang : 'en'
  if (typeof document !== 'undefined') document.documentElement.lang = i18n.lang
}

// t('key') or t('key', [a, b]) / t('key', { n }) fills {0}, {1} / {n}. A
// value of the form { one, other } is picked by the first number given.
export function t(key, params) {
  let msg = catalogs[i18n.lang]?.[key] ?? en[key] ?? key
  if (msg && typeof msg === 'object') {
    const values = Array.isArray(params) ? params : Object.values(params || {})
    const n = values.find((v) => typeof v === 'number')
    msg = n === 1 && msg.one !== undefined ? msg.one : msg.other
  }
  if (params) msg = msg.replace(/\{(\w+)\}/g, (m, k) => (params[k] ?? m))
  if (msg.includes('{server}')) msg = msg.replaceAll('{server}', i18n.server)
  return msg
}

// setServer names the linked media server for {server} (plex, jellyfin, emby).
export function setServer(platform) {
  i18n.server = { jellyfin: 'Jellyfin', emby: 'Emby' }[platform] || 'Plex'
}

// locale is the BCP 47 tag for Intl formatting.
export const locale = () => (i18n.lang === 'de' ? 'de-DE' : 'en-US')
