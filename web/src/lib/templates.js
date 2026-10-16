import { t } from './i18n.svelte.js'

// Starting points for new channels. Each returns a fresh draft.
const r = (field, op, ...values) => ({ field, op, values })
const base = (extra) => ({ order: 'shuffle', keepEpisodeOrder: true, noRepeatHours: 48, transcode: 'default', logoStyle: 'auto', ...extra })

export const kinds = [
  { kind: 'smart', icon: 'filter', get title() { return t('templates.kind.smart.title') }, get text() { return t('templates.kind.smart.text') }, color: 'var(--accent)' },
  { kind: 'manual', icon: 'list', get title() { return t('templates.kind.manual.title') }, get text() { return t('templates.kind.manual.text') }, color: 'var(--cyan)' },
  { kind: 'seasonal', icon: 'leaf', get title() { return t('templates.kind.seasonal.title') }, get text() { return t('templates.kind.seasonal.text') }, color: 'var(--green)' },
  { kind: 'ai', icon: 'sparkles', get title() { return t('templates.kind.ai.title') }, get text() { return t('templates.kind.ai.text') }, color: 'var(--violet)' },
]

export const templates = [
  { id: 'comedy', kind: 'smart', emoji: '😂', get name() { return t('templates.comedy.name') }, get text() { return t('templates.comedy.text') },
    config: () => base({ source: { type: 'rules', rules: { match: 'all', rules: [r('type', 'is', 'movie'), r('genre', 'is', 'Komödie', 'Comedy')], groups: [] } } }) },
  { id: '80s', kind: 'smart', emoji: '📼', get name() { return t('templates.80s.name') }, get text() { return t('templates.80s.text') },
    config: () => base({ logoStyle: 'retro', source: { type: 'rules', rules: { match: 'all', rules: [r('type', 'is', 'movie'), r('decade', 'is', '1980')], groups: [] } } }) },
  { id: 'unwatched', kind: 'smart', emoji: '👀', get name() { return t('templates.unwatched.name') }, get text() { return t('templates.unwatched.text') },
    config: () => base({ source: { type: 'rules', rules: { match: 'all', rules: [r('type', 'is', 'movie'), r('watched', 'is', 'false')], groups: [] } } }) },
  { id: 'new', kind: 'smart', emoji: '🆕', get name() { return t('templates.new.name') }, get text() { return t('templates.new.text') },
    config: () => base({ source: { type: 'rules', rules: { match: 'all', rules: [r('addedDays', 'lt', '30')], groups: [] } } }) },
  { id: 'family', kind: 'smart', emoji: '👨‍👩‍👧', get name() { return t('templates.family.name') }, get text() { return t('templates.family.text') },
    config: () => base({ logoStyle: 'kids', source: { type: 'rules', rules: { match: 'all', rules: [], groups: [
      { match: 'any', rules: [r('genre', 'is', 'Familie', 'Family', 'Animation', 'Kinder')], groups: [] },
      { match: 'none', rules: [r('contentRating', 'contains', '16', '18')], groups: [] },
    ] } } }) },
  { id: 'sitcom', kind: 'smart', emoji: '🛋️', get name() { return t('templates.sitcom.name') }, get text() { return t('templates.sitcom.text') },
    config: () => base({ keepEpisodeOrder: true, noRepeatHours: 0, source: { type: 'rules', rules: { match: 'all', rules: [r('type', 'is', 'episode'), r('genre', 'is', 'Komödie', 'Comedy', 'Sitcom')], groups: [] } } }) },
  { id: 'saga', kind: 'manual', emoji: '🧙', get name() { return t('templates.saga.name') }, get text() { return t('templates.saga.text') },
    config: () => base({ order: 'fixed', noRepeatHours: 0, source: { type: 'manual', items: [] } }) },
  { id: 'seasonal', kind: 'seasonal', emoji: '🎄', get name() { return t('templates.seasonal.name') }, get text() { return t('templates.seasonal.text') }, preset: true },
  { id: 'twist', kind: 'ai', emoji: '🤯', get name() { return t('templates.twist.name') }, get text() { return t('templates.twist.text') },
    config: () => base({ source: { type: 'ai', ai: { theme: t('templates.twist.theme'), batchSize: 12, episodesPerShow: 2 } } }) },
]

export const emptyConfig = (kind) => {
  if (kind === 'manual') return base({ order: 'fixed', noRepeatHours: 0, source: { type: 'manual', items: [] } })
  if (kind === 'ai') return base({ source: { type: 'ai', ai: { theme: '', batchSize: 12, episodesPerShow: 2 } } })
  if (kind === 'seasonal') return base({ seasons: [], fallback: { type: 'rules', rules: { match: 'all', rules: [], groups: [] } } })
  return base({ source: { type: 'rules', rules: { match: 'all', rules: [r('type', 'is', 'movie')], groups: [] } } })
}

// The editor picks the draft up from here after navigating.
export const pending = { draft: null }
