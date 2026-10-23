<script>
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  import Segmented from './Segmented.svelte'
  import SourceEditor from './SourceEditor.svelte'
  import { app } from '../lib/state.svelte.js'
  import { seasonLogoUrl } from '../lib/logo.js'

  // the channel's name, number, color, logo hint and uploaded logo are needed for the season logos
  let { config, name = '', number = 0, color = '', hint = '', customLogo = '', onpreview } = $props()

  const months = $derived(t('seasonEditor.months').split(','))
  const palette = ['#ef4444', '#38bdf8', '#f472b6', '#4ade80', '#facc15', '#fb923c', '#a78bfa', '#2dd4bf']
  let open = $state(null)

  const today = new Date()
  const todayKey = (today.getMonth() + 1) * 100 + today.getDate()

  function parse(s) {
    const [m, d] = (s || '01-01').split('-').map(Number)
    return { m: m || 1, d: d || 1 }
  }
  function fmt(m, d) {
    return `${String(m).padStart(2, '0')}-${String(d).padStart(2, '0')}`
  }
  function dayOfYear(s) {
    const { m, d } = parse(s)
    return (Date.UTC(2025, m - 1, d) - Date.UTC(2025, 0, 1)) / 864e5
  }
  function active(s) {
    const a = parse(s.from), b = parse(s.to)
    const from = a.m * 100 + a.d, to = b.m * 100 + b.d
    return from <= to ? todayKey >= from && todayKey <= to : todayKey >= from || todayKey <= to
  }
  // First matching season wins, like on the server.
  const current = $derived(config.seasons.find((s) => active(s)))

  function segments(s) {
    const a = dayOfYear(s.from), b = dayOfYear(s.to) + 1
    if (a <= b) return [[a, b]]
    return [[a, 365], [0, b]]
  }
  function add() {
    const id = 'season-' + Math.random().toString(36).slice(2, 8)
    config.seasons.push({ id, name: t('seasonEditor.newSeason'), emoji: '✨', from: '01-01', to: '01-31', source: { type: 'rules', rules: { match: 'any', rules: [], groups: [] } } })
    open = id
  }
  function move(i, dir) {
    const j = i + dir
    if (j < 0 || j >= config.seasons.length) return
    const [s] = config.seasons.splice(i, 1)
    config.seasons.splice(j, 0, s)
  }
  const logoStyles = $derived(app.meta?.logoStyles || [])

  function setType(s, type) {
    s.source = type === 'rules' ? { type: 'rules', rules: { match: 'any', rules: [], groups: [] } }
      : type === 'ai' ? { type: 'ai', ai: { theme: s.name, batchSize: 12, episodesPerShow: 2 } }
      : { type: 'manual', items: [] }
  }
</script>

<div class="year">
  {#each months as m, i}
    <span class="month" style="left: {(i / 12) * 100}%">{m}</span>
  {/each}
  <div class="lanes">
    {#each config.seasons as s, i (s.id)}
      {#each segments(s) as [a, b]}
        <button class="bar" class:current={current === s} title="{s.name}: {s.from} – {s.to}"
          style="left: {(a / 365) * 100}%; width: {((b - a) / 365) * 100}%; top: {(i % 3) * 14}px; background: {palette[i % palette.length]}"
          onclick={() => (open = s.id)}>{s.emoji}</button>
      {/each}
    {/each}
  </div>
  <div class="today" style="left: {(dayOfYear(fmt(today.getMonth() + 1, today.getDate())) / 365) * 100}%"><span>{t('seasonEditor.heute')}</span></div>
</div>

<p class="hint">{t('seasonEditor.ueberschneiden_sich_zeitraeume_gewinnt_d')}</p>

<div class="seasons">
  {#each config.seasons as s, i (s.id)}
    {@const f = parse(s.from)}
    {@const to = parse(s.to)}
    <div class="season" class:open={open === s.id}>
      <div class="season-head">
        <button class="title" onclick={() => (open = open === s.id ? null : s.id)}>
          <span class="emoji">{s.emoji || '•'}</span>
          <span class="grow">
            <strong>{s.name}</strong>
            {#if current === s}<span class="chip green">{t('seasonEditor.laeuft_gerade')}</span>{/if}
            <span class="muted small block">{f.d}. {months[f.m - 1]} – {to.d}. {months[to.m - 1]} · {s.source.type === 'ai' ? t('seasonEditor.sourceAI') : s.source.type === 'manual' ? t('seasonEditor.sourceManual') : t('seasonEditor.sourceRules')}</span>
          </span>
        </button>
        <button class="btn ghost icon sm" onclick={() => move(i, -1)} disabled={i === 0} title={t('seasonEditor.hoehere_prioritaet')}><Icon name="up" size={15} /></button>
        <button class="btn ghost icon sm" onclick={() => move(i, 1)} disabled={i === config.seasons.length - 1} title={t('seasonEditor.niedrigere_prioritaet')}><Icon name="down" size={15} /></button>
        <button class="btn ghost icon sm" onclick={() => onpreview?.(s.source)} title={t('seasonEditor.vorschau')}><Icon name="eye" size={15} /></button>
        <button class="btn ghost icon sm" onclick={() => config.seasons.splice(i, 1)} title={t('seasonEditor.entfernen')}><Icon name="trash" size={15} /></button>
      </div>
      {#if open === s.id}
        <div class="season-body">
          <div class="meta">
            <div class="field emoji-field">
              <label class="label" for="e-{s.id}">{t('seasonEditor.symbol')}</label>
              <input id="e-{s.id}" class="input" bind:value={s.emoji} maxlength="4" />
            </div>
            <div class="field grow">
              <label class="label" for="n-{s.id}">{t('seasonEditor.name')}</label>
              <input id="n-{s.id}" class="input" bind:value={s.name} />
            </div>
            <div class="field">
              <span class="label">{t('seasonEditor.von')}</span>
              <div class="row">
                <select class="input" value={f.d} onchange={(e) => (s.from = fmt(f.m, +e.currentTarget.value))}>
                  {#each Array(31) as _, d}<option value={d + 1}>{d + 1}.</option>{/each}
                </select>
                <select class="input" value={f.m} onchange={(e) => (s.from = fmt(+e.currentTarget.value, f.d))}>
                  {#each months as m, mi}<option value={mi + 1}>{m}</option>{/each}
                </select>
              </div>
            </div>
            <div class="field">
              <span class="label">{t('seasonEditor.bis')}</span>
              <div class="row">
                <select class="input" value={to.d} onchange={(e) => (s.to = fmt(to.m, +e.currentTarget.value))}>
                  {#each Array(31) as _, d}<option value={d + 1}>{d + 1}.</option>{/each}
                </select>
                <select class="input" value={to.m} onchange={(e) => (s.to = fmt(+e.currentTarget.value, to.d))}>
                  {#each months as m, mi}<option value={mi + 1}>{m}</option>{/each}
                </select>
              </div>
            </div>
          </div>
          <Segmented value={s.source.type} onchange={(v) => setType(s, v)} size="sm" options={[
            { value: 'rules', label: t('seasonEditor.sourceRules'), icon: 'filter' },
            { value: 'ai', label: t('seasonEditor.sourceAI'), icon: 'sparkles' },
            { value: 'manual', label: t('seasonEditor.manual'), icon: 'list' },
          ]} />
          {#key s.source}
            <SourceEditor source={s.source} />
          {/key}
          <div class="season-logo">
            <div class="field">
              <label class="label" for="l-{s.id}">{t('seasonEditor.logo_stil')}</label>
              <select id="l-{s.id}" class="input" value={s.logoStyle || ''}
                onchange={(e) => { const v = e.currentTarget.value; if (v) s.logoStyle = v; else delete s.logoStyle }}>
                <option value="">{t('seasonEditor.standard_sender_logo')}</option>
                {#each logoStyles as st (st.id)}<option value={st.id}>{st.label}{st.id === 'auto' ? t('seasonEditor.autoSuffix') : ''}</option>{/each}
              </select>
              <span class="muted tiny">{s.logoStyle ? t('seasonEditor.logoLikeChannel') : config.logoHoliday && !customLogo && s.emoji?.trim() ? t('seasonEditor.channelLogoWith', [s.emoji.trim()]) : t('seasonEditor.channelLogo')}</span>
            </div>
            <div class="logo-frame"><img src={seasonLogoUrl({ name, number, color, config, hint, customLogo }, s)} alt="" /></div>
          </div>
        </div>
      {/if}
    </div>
  {/each}
</div>

<button class="btn" onclick={add}><Icon name="plus" size={15} /> {t('seasonEditor.saison_hinzufuegen')}</button>

<style>
  .year { position: relative; height: 72px; margin: 0.5rem 0 0.25rem; border-radius: var(--radius-sm); background: var(--bg-2); border: 1px solid var(--border); overflow: hidden; }
  .month { position: absolute; top: 4px; font-size: 0.68rem; color: var(--muted); padding-left: 4px; border-left: 1px solid var(--border); height: 100%; }
  .lanes { position: absolute; left: 0; right: 0; top: 24px; height: 44px; }
  .bar { position: absolute; height: 12px; border-radius: 4px; border: 0; padding: 0; font-size: 0.6rem; line-height: 12px; cursor: pointer; opacity: 0.75; overflow: hidden; }
  .bar.current { opacity: 1; box-shadow: 0 0 0 2px white; }
  .today { position: absolute; top: 0; bottom: 0; width: 2px; background: var(--accent); }
  .today span { position: absolute; bottom: 2px; left: 4px; font-size: 0.65rem; color: var(--accent-2); font-weight: 700; }
  .seasons { display: flex; flex-direction: column; gap: 0.5rem; margin: 0.75rem 0; }
  .season { border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-2); }
  .season.open { border-color: var(--border-strong); }
  .season-head { display: flex; align-items: center; gap: 0.25rem; padding: 0.4rem 0.5rem; }
  .title { display: flex; align-items: center; gap: 0.7rem; flex: 1; border: 0; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; padding: 0.2rem; }
  .emoji { font-size: 1.5rem; width: 2rem; text-align: center; }
  .block { display: block; }
  .season-body { padding: 0.75rem 1rem 1rem; border-top: 1px solid var(--border); display: flex; flex-direction: column; gap: 0.9rem; }
  .meta { display: flex; gap: 0.75rem; flex-wrap: wrap; align-items: flex-end; }
  .emoji-field { width: 70px; }
  .emoji-field .input { text-align: center; font-size: 1.1rem; }
  .meta select { width: auto; }
  .season-logo { border-top: 1px solid var(--border); padding-top: 0.9rem; display: flex; gap: 1rem; align-items: center; flex-wrap: wrap; }
  .season-logo .field { flex: 1; min-width: 220px; gap: 0.3rem; }
  .season-logo .logo-frame { width: 160px; aspect-ratio: 16 / 9; border-radius: 6px; display: grid; place-items: center; overflow: hidden;
    background: radial-gradient(circle at 50% 40%, #2a2c38, #15161c); }
  .season-logo .logo-frame img { width: 100%; height: 100%; object-fit: contain; }
</style>
