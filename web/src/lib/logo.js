import { t } from './i18n.svelte.js'

// URL of a rendered logo preview with all automatic-logo options.
export function logoPreviewUrl({ name, style, color, hint, seed, hue, tagline, number, holiday, symbol }) {
  const q = new URLSearchParams({
    name: name || t('logo.defaultName'),
    style: style || 'auto',
    color: color || '',
    hint: hint || '',
    seed: String(seed || 0),
    hue: String(hue || 0),
    tagline: tagline || '',
    number: String(number || 0),
    holiday: holiday || '',
    symbol: symbol || '',
  })
  return `/api/logo/preview?${q}`
}

// Words that steer the automatic logo style of a season (like the server).
export const seasonHint = (season) => [season.name, season.source?.ai?.theme || ''].join(' ').trim()

// The logo a seasonal channel shows during a season, mirroring the server: "Standard"
// keeps the channel's logo (uploaded or generated, with the season symbol; its automatic
// style follows the season), otherwise it is generated in the season's style.
export function seasonLogoUrl({ name, number, color, config, hint, customLogo }, season) {
  const own = !season.logoStyle
  if (own && customLogo) return customLogo
  return logoPreviewUrl({
    name, color, seed: config.logoSeed, tagline: config.logoTagline, number: config.logoNumber ? number : 0,
    symbol: config.logoHoliday ? (season.emoji || '').trim() : '',
    ...(own ? { style: config.logoStyle, hint: `${seasonHint(season)} ${hint}`, hue: config.logoHue } : { style: season.logoStyle, hint: seasonHint(season) }),
  })
}

export const holidays = ['', 'christmas', 'newyear', 'valentine', 'easter', 'halloween'].map((value) => ({
  value,
  get label() { return t(`logo.holiday.${value || 'none'}`) },
}))

// Colour themes as hue rotations; 0 keeps the original colours of a style.
export const hueThemes = [0, 30, 60, 120, 180, 210, 260, 300]
