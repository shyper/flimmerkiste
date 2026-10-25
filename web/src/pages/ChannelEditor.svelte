<script>
  import { t } from '../lib/i18n.svelte.js'
  import { onMount, onDestroy } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import Toggle from '../components/Toggle.svelte'
  import Modal from '../components/Modal.svelte'
  import SourceEditor from '../components/SourceEditor.svelte'
  import SeasonEditor from '../components/SeasonEditor.svelte'
  import LogoPicker from '../components/LogoPicker.svelte'
  import OrderSection from '../components/editor/OrderSection.svelte'
  import BreaksSection from '../components/editor/BreaksSection.svelte'
  import TechSection from '../components/editor/TechSection.svelte'
  import PreviewCard from '../components/editor/PreviewCard.svelte'
  import { app, toast } from '../lib/state.svelte.js'
  import { api, get, post, put, del } from '../lib/api.js'
  import { navigate, link, leaveGuard } from '../lib/router.svelte.js'
  import { kindLabels, kindIcons } from '../lib/format.js'
  import { emptyConfig, pending, kinds } from '../lib/templates.js'
  import { logoPreviewUrl, seasonLogoUrl } from '../lib/logo.js'
  import { normalizeConfig } from '../lib/config.js'
  import { poll } from '../lib/poll.js'
  import LoadError from '../components/LoadError.svelte'

  let { id = null, kind = null } = $props()

  let ch = $state(null)
  let saving = $state(false)
  // Saving keeps the planned guide; programme changes wait for "Neu planen".
  let planPending = $state(false)
  let preview = $state(null)
  let previewLoading = $state(false)
  let previewSource = $state(null) // seasonal: the season being previewed
  let upcoming = $state([])
  let confirmDelete = $state(false)
  let logoUrl = $state('')
  let pendingLogo = null // file chosen before the channel exists; uploaded right after creation
  let error = $state('')
  let loadFailed = $state('')
  // The server plans in the background (AI requests take minutes); while it
  // does, the editor polls more often and reloads "Up next" once it is done.
  let planning = $state(false)
  let errorPoll
  let savedTimer
  let destroyed = false

  // prepared completes a channel before it is shown: the sections bind to every field
  function prepared(c) {
    c.enabled ??= true
    normalizeConfig(c.config, c.kind)
    return c
  }

  async function open() {
    loadFailed = ''
    let c
    try {
      c = await get(`/api/channels/${id}`)
    } catch (e) {
      if (!destroyed) loadFailed = e.message
      return false
    }
    if (destroyed) return false
    ch = prepared({ id: c.id, name: c.name, number: c.number, color: c.color, enabled: c.enabled, kind: c.kind, config: c.config || {} })
    logoUrl = c.customLogoUrl || ''
    error = c.error || ''
    planning = !!c.planning
    app.missing = c.missing || []
    loadUpcoming()
    errorPoll = poll(loadError, () => (planning ? 3000 : 30000))
    return true
  }

  async function init() {
    if (id) {
      if (!(await open())) return
    } else {
      const d = pending.draft?.kind === kind ? pending.draft : { kind, name: '', config: emptyConfig(kind) }
      pending.draft = null
      app.missing = []
      ch = prepared({ name: d.name, number: '', color: '', enabled: true, kind: d.kind, config: d.config })
    }
    // after the sections filled in their defaults (until then nothing counts as changed)
    savedTimer = setTimeout(() => (saved = snapshot()), 0)
  }
  onMount(init)

  // Unsaved changes: a bar offers saving, leaving asks first.
  let saved = $state('')
  const snapshot = () => JSON.stringify(ch && [ch.name, ch.number, ch.enabled, ch.color, ch.config])
  const dirty = $derived(!!saved && snapshot() !== saved)
  function onBeforeUnload(e) {
    if (dirty) e.preventDefault()
  }
  onMount(() => {
    window.addEventListener('beforeunload', onBeforeUnload)
    leaveGuard.hold(guard)
  })
  const guard = (to) => !dirty || confirm(t('channelEditor.leaveConfirm'))

  onDestroy(() => {
    destroyed = true
    errorPoll?.stop()
    clearTimeout(savedTimer)
    clearTimeout(previewTimer)
    window.removeEventListener('beforeunload', onBeforeUnload)
    leaveGuard.release(guard)
  })

  const jumps = $derived([
    ['basics', t('channelEditor.grunddaten')], ['content', t('channelEditor.inhalt')], ['order', t('channelEditor.ablauf')],
    ['breaks', t('channelEditor.sendezeiten_pausen')], ['logo', t('channelEditor.logo')], ['tech', t('channelEditor.technik')],
  ])
  const jump = (id) => document.getElementById(`sec-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })

  // The planning problem disappears by itself once the next run succeeds.
  async function loadError() {
    if (!ch?.id) return
    const r = await get(`/api/channels/${ch.id}/error`).catch(() => null)
    if (!r || destroyed) return
    error = r.error ?? error
    const was = planning
    planning = !!r.planning
    if (was && !planning) loadUpcoming()
  }

  const usesAI = $derived(!!ch && [ch.config.source, ch.config.fallback, ...(ch.config.seasons || []).map((s) => s.source)]
    .some((s) => s?.type === 'ai'))

  async function dismissError() {
    try {
      await del(`/api/channels/${ch.id}/error`)
      error = ''
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  // Remove manual entries that no longer exist in Plex from every source of the channel.
  function removeMissing() {
    const gone = new Set(app.missing)
    const clean = (src) => {
      if (src?.type === 'manual') src.items = src.items.filter((it) => !gone.has(`${it.type}|${it.key}|${it.season || 0}`))
    }
    clean(ch.config.source)
    ch.config.seasons?.forEach((s) => clean(s.source))
    clean(ch.config.fallback)
    clean(ch.config.filler)
    toast(t('channelEditor.entriesRemoved', [gone.size]))
    app.missing = []
  }

  async function loadUpcoming() {
    const rows = await get(`/api/guide?hours=24&from=${Date.now()}&channel=${ch.id}&limit=12`).catch(() => null)
    if (!rows || destroyed) return
    upcoming = rows.find((r) => r.channel.id === ch.id)?.programs.slice(0, 12) || []
  }

  // The season airing today (first match wins, like on the server).
  const currentSeason = $derived.by(() => {
    if (ch?.kind !== 'seasonal') return null
    const d = new Date()
    const today = (d.getMonth() + 1) * 100 + d.getDate()
    const key = (s) => { const [m, dd] = (s || '01-01').split('-').map(Number); return m * 100 + dd }
    return ch.config.seasons?.find((s) => {
      const from = key(s.from), to = key(s.to)
      return from <= to ? today >= from && today <= to : today >= from || today <= to
    }) || null
  })

  // ---- live preview of the selected content ----
  const activeSource = $derived.by(() => {
    if (!ch) return null
    if (ch.kind === 'seasonal') return previewSource || currentSeason?.source || ch.config.fallback
    return ch.config.source
  })
  let previewTimer
  // only the answer to the newest request is shown: an older, slower one may arrive later
  let previewRequest = 0
  $effect(() => {
    if (!activeSource) return
    const body = JSON.stringify({ source: $state.snapshot(activeSource), sortBy: ch.config.sortBy || '' })
    clearTimeout(previewTimer)
    previewTimer = setTimeout(async () => {
      const n = ++previewRequest
      previewLoading = true
      try {
        const res = await api('POST', '/api/preview', JSON.parse(body))
        if (n === previewRequest) preview = res
      } catch (e) {
        if (n === previewRequest) preview = null
      } finally {
        if (n === previewRequest) previewLoading = false
      }
    }, 450)
  })

  const logoHint = $derived.by(() => {
    if (!ch) return ''
    const words = []
    const walk = (g) => {
      g?.rules?.forEach((r) => (r.field === 'genre' || r.field === 'collection') && words.push(...r.values))
      g?.rules?.forEach((r) => r.field === 'decade' && words.push(...r.values.map((v) => v.slice(2) + 'er')))
      g?.groups?.forEach(walk)
    }
    walk(ch.config.source?.rules)
    if (ch.config.source?.ai?.theme) words.push(ch.config.source.ai.theme)
    return words.join(' ')
  })

  async function save() {
    saving = true
    try {
      const body = { name: ch.name, number: Number(ch.number) || 0, enabled: ch.enabled, kind: ch.kind, color: ch.color, config: $state.snapshot(ch.config) }
      if (ch.id) {
        const res = await put(`/api/channels/${ch.id}`, body)
        if (res.planChanged) planPending = true
        saved = snapshot()
        toast(t('channelEditor.saved'), 'success')
      } else {
        const created = await post('/api/channels', body)
        if (pendingLogo) {
          const fd = new FormData()
          fd.append('file', pendingLogo)
          await api('POST', `/api/channels/${created.id}/logo`, fd).catch((e) => toast(e.message, 'error'))
        }
        saved = snapshot()
        toast(t('channelEditor.onAir', [created.name]), 'success')
        navigate(`/channels/${created.id}`, { replace: true })
      }
    } catch (e) {
      toast(e.message, 'error')
    } finally {
      saving = false
    }
  }

  async function remove() {
    try {
      await del(`/api/channels/${ch.id}`)
    } catch (e) {
      confirmDelete = false
      toast(e.message, 'error')
      return
    }
    saved = ''
    toast(t('channelEditor.deleted'))
    navigate('/')
  }

  async function regenerate() {
    try {
      await post(`/api/channels/${ch.id}/regenerate`)
    } catch (e) {
      toast(e.message, 'error') // the old plan stays, and so does the note
      return
    }
    planPending = false
    toast(t('channelEditor.replanning'))
    planning = true
    errorPoll?.now()
  }

  async function loadPreset() {
    try {
      const p = await get('/api/presets/seasonal')
      ch.config.seasons = p.seasons
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  async function uploadLogo(file) {
    if (!/^image\/(png|jpeg|webp|gif)$/.test(file.type)) {
      toast(t('channelEditor.fileType'), 'error')
      return
    }
    if (file.size > 3 * 1024 * 1024) {
      toast(t('channelEditor.fileSize'), 'error')
      return
    }
    if (!ch.id) {
      // Not saved yet: show it now, upload after "Sender erstellen".
      pendingLogo = file
      if (logoUrl.startsWith('blob:')) URL.revokeObjectURL(logoUrl)
      logoUrl = URL.createObjectURL(file)
      return
    }
    const fd = new FormData()
    fd.append('file', file)
    try {
      const c = await api('POST', `/api/channels/${ch.id}/logo`, fd)
      logoUrl = c.customLogoUrl || ''
      toast(t('channelEditor.logoUploaded'), 'success')
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  async function deleteLogo() {
    if (!ch.id) {
      pendingLogo = null
      URL.revokeObjectURL(logoUrl)
      logoUrl = ''
      return
    }
    try {
      await del(`/api/channels/${ch.id}/logo`)
      logoUrl = ''
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  // A manual channel without titles plays only its trailers or clips, around the clock.
  const fillerOnly = $derived(ch?.kind === 'manual' && !ch.config.source?.items?.length)
  // "default" follows the global setting (Einstellungen → Streaming).
  const effectiveTranscode = $derived(ch?.config.transcode === 'default' ? app.meta?.defaultTranscode || 'copy' : ch?.config.transcode)

  const sideLogo = $derived(ch ? logoPreviewUrl({
    name: ch.name, style: ch.config.logoStyle, color: ch.color, hint: logoHint, seed: ch.config.logoSeed, hue: ch.config.logoHue,
    tagline: ch.config.logoTagline, number: ch.config.logoNumber ? Number(ch.number) || 0 : 0,
  }) : '')
  // what the channel shows right now: during a season the season's logo, otherwise its own
  const currentLogo = $derived(ch?.kind === 'seasonal' && currentSeason
    ? seasonLogoUrl({ name: ch.name, number: Number(ch.number) || 0, color: ch.color, config: ch.config, hint: logoHint, customLogo: logoUrl }, currentSeason)
    : logoUrl || sideLogo)

  const kindInfo = $derived(kinds.find((k) => k.kind === ch?.kind))
</script>

{#if !ch && loadFailed}
  <div class="page"><LoadError message={loadFailed} retry={init} /></div>
{:else if !ch}
  <div class="page"><div class="skeleton" style="height: 400px"></div></div>
{:else}
  <div class="page">
    <div class="page-head">
      <a class="btn ghost icon" href="/" use:link aria-label={t('channelEditor.zurueck')}><Icon name="left" /></a>
      <div class="grow">
        <div class="row tiny muted"><Icon name={kindIcons[ch.kind]} size={13} /> {t('channelEditor.er_sender', [kindLabels[ch.kind]])}</div>
        <h1>{ch.id ? ch.name || t('channelEditor.channel') : t('channelEditor.newChannel')}</h1>
      </div>
      {#if ch.id}
        <button class="btn" onclick={regenerate} disabled={planning} title={t('channelEditor.zukuenftiges_programm_verwerfen_und_neu')}><Icon name="refresh" size={16} /> {t('channelEditor.neu_planen')}</button>
      {/if}
      <button class="btn primary" onclick={save} disabled={saving || !ch.name.trim()}>
        {#if saving}<span class="spinner"></span>{:else}<Icon name="check" size={16} />{/if}
        {ch.id ? t('channelEditor.save') : t('channelEditor.create')}
      </button>
    </div>

    {#if error}
      <div class="alert error mb">
        <Icon name="alert" size={16} />
        <span class="grow">{error}</span>
        <button class="btn ghost icon sm" onclick={dismissError} title={t('channelEditor.meldung_schliessen')} aria-label={t('channelEditor.meldung_schliessen')}><Icon name="x" size={15} /></button>
      </div>
    {/if}
    {#if planPending}
      <div class="alert info mb">
        <Icon name="clock" size={16} />
        <span class="grow">{t('channelEditor.die_aenderungen_am_programm_gelten', [app.meta?.horizon || 48])}</span>
        <button class="btn sm" onclick={regenerate} disabled={planning}><Icon name="refresh" size={14} /> {t('channelEditor.jetzt_neu_planen')}</button>
      </div>
    {/if}
    {#if app.missing.length}
      <div class="alert warn mb">
        <Icon name="alert" size={16} />
        <span class="grow">{app.missing.length === 1 ? t('channelEditor.missingOne') : t('channelEditor.missingMany', [app.missing.length])}</span>
        <button class="btn sm" onclick={removeMissing}><Icon name="trash" size={14} /> {t('channelEditor.aus_der_liste_entfernen')}</button>
      </div>
    {/if}

    <nav class="jumps" aria-label={t('channelEditor.sections')}>
      {#each jumps as [id, label]}<button class="chip" onclick={() => jump(id)}>{label}</button>{/each}
    </nav>

    <div class="layout">
      <div class="main stack">
        <section class="card" id="sec-basics">
          <div class="section-title"><Icon name="tv" /><h2>{t('channelEditor.grunddaten')}</h2></div>
          <div class="basics">
            <div class="field name">
              <label class="label" for="name">{t('channelEditor.sendername')}</label>
              <input id="name" class="input big" bind:value={ch.name} placeholder={t('channelEditor.z_b_sonntagskino')} />
            </div>
            <div class="field">
              <label class="label" for="num">{t('channelEditor.kanal')}</label>
              <input id="num" class="input" type="number" min="1" bind:value={ch.number} placeholder={t('channelEditor.auto')} />
            </div>
          </div>
          <div class="mt"><Toggle bind:checked={ch.enabled} label={t('channelEditor.sender_aktiv')} hint={t('channelEditor.inaktive_sender_tauchen_in_plex')} /></div>
        </section>

        <section class="card" id="sec-content">
          <div class="section-title"><Icon name={kindIcons[ch.kind]} /><h2>{t('channelEditor.inhalt')}</h2></div>
          {#if ch.kind === 'smart'}
            <p class="muted small mb">{t('channelEditor.lege_fest_welche_titel_auf')}</p>
            <SourceEditor source={ch.config.source} />
          {:else if ch.kind === 'manual'}
            <SourceEditor source={ch.config.source} />
          {:else if ch.kind === 'ai'}
            <p class="muted small mb">{kindInfo?.text}</p>
            <SourceEditor source={ch.config.source} />
          {:else if ch.kind === 'seasonal'}
            <p class="muted small mb">{t('channelEditor.der_sender_wechselt_automatisch_sein')}</p>
            {#if !ch.config.seasons.length}
              <div class="alert info mb">
                <Icon name="info" size={16} />
                <span class="grow">{t('channelEditor.noch_keine_saisons_angelegt')}</span>
                <button class="btn sm" onclick={loadPreset}>
                  <Icon name="wand" size={14} /> {t('channelEditor.jahreskalender_laden')} </button>
              </div>
            {/if}
            <SeasonEditor config={ch.config} name={ch.name} number={Number(ch.number) || 0} color={ch.color} hint={logoHint} customLogo={logoUrl} onpreview={(s) => (previewSource = s)} />
            <div class="fallback">
              <h3>{t('channelEditor.restliche_zeit')}</h3>
              <p class="muted small mb">{t('channelEditor.laeuft_wenn_keine_saison_aktiv')}</p>
              <div class="row mb"><button class="btn sm ghost" onclick={() => (previewSource = ch.config.fallback)}><Icon name="eye" size={14} /> {t('channelEditor.vorschau')}</button></div>
              <SourceEditor source={ch.config.fallback} />
            </div>
          {/if}
        </section>

        <OrderSection bind:ch />

        <BreaksSection bind:ch {fillerOnly} {effectiveTranscode} />

        <section class="card" id="sec-logo">
          <div class="section-title"><Icon name="sparkles" /><h2>{t('channelEditor.logo')}</h2></div>
          {#if ch.kind === 'seasonal'}
            <p class="muted small mb">{t('channelEditor.gilt_ausserhalb_der_saisons_und')}</p>
          {/if}
          <LogoPicker name={ch.name} config={ch.config} seasonal={ch.kind === 'seasonal'} number={Number(ch.number) || 0} bind:color={ch.color} hint={logoHint}
            customLogo={logoUrl} posterSource={activeSource ? $state.snapshot(activeSource) : null} onupload={uploadLogo} ondelete={deleteLogo} />
        </section>

        <TechSection bind:ch {currentLogo} />

        {#if ch.id}
          <section class="card danger-zone">
            <div class="row">
              <div class="grow"><h3>{t('channelEditor.sender_loeschen')}</h3><p class="small muted">{t('channelEditor.entfernt_den_sender_samt_programm')}</p></div>
              <button class="btn danger" onclick={() => (confirmDelete = true)}><Icon name="trash" size={16} /> {t('channelEditor.loeschen')}</button>
            </div>
          </section>
        {/if}
      </div>

      <aside class="side">
        <PreviewCard {ch} {currentLogo} {preview} {previewLoading} {previewSource} {currentSeason} {planning} {usesAI} {upcoming} />
      </aside>
    </div>

    {#if dirty}
      <div class="savebar" role="region" aria-label={t('channelEditor.unsaved')}>
        <span class="grow"><Icon name="alert" size={16} /> {t('channelEditor.unsaved')}</span>
        <button class="btn primary" onclick={save} disabled={saving || !ch.name.trim()}>
          {#if saving}<span class="spinner"></span>{:else}<Icon name="check" size={16} />{/if}
          {ch.id ? t('channelEditor.save') : t('channelEditor.create')}
        </button>
      </div>
    {/if}
  </div>

  <Modal bind:open={confirmDelete} title={t('channelEditor.sender_loeschen_2')}>
    <p>{t('channelEditor.wird_endgueltig_geloescht', [ch.name])}</p>
    {#snippet footer()}
      <button class="btn" onclick={() => (confirmDelete = false)}>{t('channelEditor.abbrechen')}</button>
      <button class="btn danger" onclick={remove}><Icon name="trash" size={16} /> {t('channelEditor.loeschen')}</button>
    {/snippet}
  </Modal>
{/if}

<style>
  .layout { display: grid; grid-template-columns: minmax(0, 1fr) 340px; gap: 1.25rem; align-items: start; }
  .jumps { display: flex; flex-wrap: wrap; gap: 0.35rem; margin-bottom: 1rem; }
  .jumps .chip { border: 0; cursor: pointer; font: inherit; font-size: 0.8rem; }
  .jumps .chip:hover { color: var(--accent-2); }
  .main :global(section[id]) { scroll-margin-top: 1rem; }
  .savebar { position: sticky; bottom: 1rem; margin-top: 1rem; display: flex; align-items: center; gap: 0.6rem; padding: 0.7rem 1rem; border-radius: var(--radius-sm); background: var(--surface-2); border: 1px solid var(--accent); box-shadow: var(--shadow); z-index: 20; }
  .mb { margin-bottom: 1rem; }
  .mt { margin-top: 1rem; }
  .basics { display: grid; grid-template-columns: 1fr 110px; gap: 1rem; }
  .input.big { font-size: 1.15rem; font-weight: 700; padding: 0.7rem 0.9rem; }
  .fallback { margin-top: 1.5rem; padding-top: 1.25rem; border-top: 1px solid var(--border); }
  .danger-zone { border-color: rgba(239, 68, 68, 0.25); }
  @media (max-width: 1100px) {
    .layout { grid-template-columns: 1fr; }
    .side { order: -1; }
  }
</style>
