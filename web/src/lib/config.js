// Completes channel configurations of any age before the editor binds to them.
// A live database keeps what older versions stored, and components such as
// Toggle, TagInput and MediaPicker fail on a missing bound value. Every field
// the editor binds therefore gets its default here – also for new fields.

export const emptyRules = () => ({ match: 'all', rules: [], groups: [] })

export function normalizeRules(g) {
  g ??= emptyRules()
  if (!['all', 'any', 'none'].includes(g.match)) g.match = 'all'
  g.rules ??= []
  g.groups ??= []
  for (const r of g.rules) r.values ??= []
  g.groups.forEach(normalizeRules)
  return g
}

export function normalizeSource(src) {
  if (!src) return src
  if (src.type === 'rules') src.rules = normalizeRules(src.rules)
  if (src.type === 'manual') src.items ??= []
  if (src.type === 'ai') {
    src.ai ??= { theme: '' }
    src.ai.theme ??= ''
    src.ai.batchSize ||= 12
    src.ai.episodesPerShow ||= 2
    if (src.ai.filter) normalizeRules(src.ai.filter)
  }
  if (src.type === 'trailers') {
    src.trailerOrigin ||= 'library'
    src.rules = normalizeRules(src.rules)
    src.channelFilter ??= false
  }
  return src
}

export function normalizeConfig(cfg, kind) {
  cfg ??= {}
  cfg.order ??= 'shuffle'
  cfg.transcode ??= 'default'
  cfg.credits ??= 'default'
  cfg.noRepeatHours ??= 48
  cfg.logoStyle ||= 'auto'
  cfg.keepEpisodeOrder ??= true
  cfg.logoSeed ??= 0
  cfg.logoHue ??= 0
  cfg.logoTagline ??= ''
  cfg.logoNumber ??= false
  cfg.logoHoliday ??= false
  cfg.align ??= 0
  cfg.fillerBetween ??= 0
  cfg.previews ??= false
  cfg.watermark ??= {}
  cfg.watermark.enabled ??= false
  cfg.watermark.position ||= 'tr'
  cfg.watermark.opacity ||= 70
  cfg.watermark.size ||= 14
  cfg.watermark.tone ||= 'color'
  cfg.source ??= { type: '' }
  normalizeSource(cfg.source)
  if (kind === 'seasonal') {
    cfg.seasons ??= []
    for (const s of cfg.seasons) {
      s.name ??= ''
      s.emoji ??= ''
      s.source ??= { type: 'rules' }
      normalizeSource(s.source)
    }
    cfg.fallback ??= { type: 'rules' }
    normalizeSource(cfg.fallback)
  }
  if (cfg.filler) normalizeSource(cfg.filler)
  return cfg
}
