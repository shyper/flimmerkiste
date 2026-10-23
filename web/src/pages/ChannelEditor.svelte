<script>
  import { t } from '../lib/i18n.svelte.js'
  import { onMount, onDestroy } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import Toggle from '../components/Toggle.svelte'
  import Segmented from '../components/Segmented.svelte'
  import Modal from '../components/Modal.svelte'
  import SourceEditor from '../components/SourceEditor.svelte'
  import SeasonEditor from '../components/SeasonEditor.svelte'
  import LogoPicker from '../components/LogoPicker.svelte'
  import { app, toast } from '../lib/state.svelte.js'
  import { api, get, post, put, del } from '../lib/api.js'
  import { navigate, link, leaveGuard } from '../lib/router.svelte.js'
  import { fmtDuration, fmtTime, fmtDay, kindLabels, kindIcons } from '../lib/format.js'
  import { emptyConfig, pending, kinds } from '../lib/templates.js'
  import { logoPreviewUrl, seasonLogoUrl } from '../lib/logo.js'
  import { normalizeConfig } from '../lib/config.js'

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
  let errorTimer

  onMount(async () => {
    if (id) {
      const c = await get(`/api/channels/${id}`)
      ch = { id: c.id, name: c.name, number: c.number, color: c.color, enabled: c.enabled, kind: c.kind, config: c.config || {} }
      logoUrl = c.customLogoUrl || ''
      error = c.error || ''
      app.missing = c.missing || []
      loadUpcoming()
      errorTimer = setInterval(loadError, 30000)
    } else {
      const d = pending.draft?.kind === kind ? pending.draft : { kind, name: '', config: emptyConfig(kind) }
      pending.draft = null
      app.missing = []
      ch = { name: d.name, number: '', color: '', enabled: true, kind: d.kind, config: d.config }
    }
    ch.enabled ??= true
    normalizeConfig(ch.config, ch.kind)
    // after the sections filled in their defaults
    setTimeout(() => (saved = snapshot()), 0)
  })

  // Unsaved changes: a bar offers saving, leaving asks first.
  let saved = $state('')
  const snapshot = () => JSON.stringify(ch && [ch.name, ch.number, ch.enabled, ch.color, ch.config])
  const dirty = $derived(!!saved && snapshot() !== saved)
  function onBeforeUnload(e) {
    if (dirty) e.preventDefault()
  }
  onMount(() => {
    window.addEventListener('beforeunload', onBeforeUnload)
    leaveGuard.check = (to) => !dirty || confirm(t('channelEditor.leaveConfirm'))
  })

  onDestroy(() => {
    clearInterval(errorTimer)
    window.removeEventListener('beforeunload', onBeforeUnload)
    leaveGuard.check = null
  })

  const jumps = $derived([
    ['basics', t('channelEditor.grunddaten')], ['content', t('channelEditor.inhalt')], ['order', t('channelEditor.ablauf')],
    ['breaks', t('channelEditor.sendezeiten_pausen')], ['logo', t('channelEditor.logo')], ['tech', t('channelEditor.technik')],
  ])
  const jump = (id) => document.getElementById(`sec-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })

  // The planning problem disappears by itself once the next run succeeds.
  async function loadError() {
    if (ch?.id) error = (await get(`/api/channels/${ch.id}/error`).catch(() => null))?.error ?? error
  }

  async function dismissError() {
    error = ''
    await del(`/api/channels/${ch.id}/error`).catch((e) => toast(e.message, 'error'))
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
    const rows = await get(`/api/guide?hours=24&from=${Date.now()}`)
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
  $effect(() => {
    if (!activeSource) return
    const body = JSON.stringify({ source: $state.snapshot(activeSource), sortBy: ch.config.sortBy || '' })
    clearTimeout(previewTimer)
    previewTimer = setTimeout(async () => {
      previewLoading = true
      try {
        preview = await api('POST', '/api/preview', JSON.parse(body))
      } catch (e) {
        preview = null
      } finally {
        previewLoading = false
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
    await del(`/api/channels/${ch.id}`)
    saved = ''
    toast(t('channelEditor.deleted'))
    navigate('/')
  }

  async function regenerate() {
    planPending = false
    await post(`/api/channels/${ch.id}/regenerate`)
    toast(t('channelEditor.replanning'))
    setTimeout(loadUpcoming, 2000)
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
    await del(`/api/channels/${ch.id}/logo`)
    logoUrl = ''
  }

  // ---- breaks: aligned start times, filler clips, up-next slate ----
  const alignOptions = [
    { value: 0, get label() { return t('channelEditor.align.0') } }, { value: 5, get label() { return t('channelEditor.align.5') } }, { value: 15, label: ':00 :15 :30 :45' },
    { value: 30, get label() { return t('channelEditor.align.30') } }, { value: 60, get label() { return t('channelEditor.align.60') } },
  ]
  let cardVersion = $state(0)
  let creditsVersion = $state(0)
  const shortClips = () => ({ type: 'rules', rules: { match: 'all', rules: [{ field: 'duration', op: 'lt', values: ['10'] }], groups: [] } })
  function toggleFiller(on) {
    ch.config.filler = on ? { type: 'trailers', trailerOrigin: 'library', rules: { match: 'all', rules: [], groups: [] } } : undefined
  }
  function setFillerType(t) {
    if (t === 'manual') ch.config.filler = { type: 'manual', items: [] }
    else if (t === 'trailers') ch.config.filler = { type: 'trailers', trailerOrigin: 'library', rules: { match: 'all', rules: [], groups: [] } }
    else ch.config.filler = shortClips()
  }
  // "default" follows the global setting (Einstellungen → Streaming).
  // A manual channel without titles plays only its trailers or clips, around the clock.
  const fillerOnly = $derived(ch?.kind === 'manual' && !ch.config.source?.items?.length)
  const effectiveTranscode = $derived(ch?.config.transcode === 'default' ? app.meta?.defaultTranscode || 'copy' : ch?.config.transcode)
  function toggleWatermark(on) {
    ch.config.watermark.enabled = on
    if (on && effectiveTranscode !== 'full') {
      ch.config.transcode = 'full'
      toast(t('channelEditor.fullForWatermark'))
    }
  }
  const wmStyle = $derived.by(() => {
    const w = ch?.config.watermark
    if (!w) return ''
    const v = w.position?.startsWith('b') ? 'bottom' : 'top'
    const h = w.position?.endsWith('l') ? 'left' : 'right'
    const tone = { gray: '; filter: grayscale(1)', white: '; filter: brightness(0) invert(1)' }[w.tone] || ''
    return `${v}: 4%; ${h}: 3%; width: ${w.size}%; opacity: ${w.opacity / 100}${tone}`
  })

  const sideLogo = $derived(ch ? logoPreviewUrl({
    name: ch.name, style: ch.config.logoStyle, color: ch.color, hint: logoHint, seed: ch.config.logoSeed, hue: ch.config.logoHue,
    tagline: ch.config.logoTagline, number: ch.config.logoNumber ? Number(ch.number) || 0 : 0,
  }) : '')
  // what the channel shows right now: during a season the season's logo, otherwise its own
  const currentLogo = $derived(ch?.kind === 'seasonal' && currentSeason
    ? seasonLogoUrl({ name: ch.name, number: Number(ch.number) || 0, color: ch.color, config: ch.config, hint: logoHint, customLogo: logoUrl }, currentSeason)
    : logoUrl || sideLogo)

  const kindInfo = $derived(kinds.find((k) => k.kind === ch?.kind))
  const repeatOptions = [
    { value: 0, get label() { return t('channelEditor.repeat.0') } }, { value: 12, get label() { return t('channelEditor.repeat.12') } }, { value: 48, get label() { return t('channelEditor.repeat.48') } },
    { value: 168, get label() { return t('channelEditor.repeat.168') } }, { value: 720, get label() { return t('channelEditor.repeat.720') } },
  ]
  const transcodeModes = [
    { value: 'copy', cpu: 1, get title() { return t('transcode.copy') }, get text() { return t('transcode.copy.text') } },
    { value: 'audio', cpu: 2, get title() { return t('transcode.audio') }, get text() { return t('transcode.audio.text') } },
    { value: 'full', cpu: 3, get title() { return t('transcode.full') }, get text() { return t('transcode.full.text') } },
  ]
  const global = $derived(transcodeModes.find((m) => m.value === (app.meta?.defaultTranscode || 'copy')))
  const effectiveCredits = $derived(ch?.config.credits === 'default' ? app.meta?.defaultCredits || 'off' : ch?.config.credits)
</script>

{#if !ch}
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
        <button class="btn" onclick={regenerate} title={t('channelEditor.zukuenftiges_programm_verwerfen_und_neu')}><Icon name="refresh" size={16} /> {t('channelEditor.neu_planen')}</button>
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
        <button class="btn sm" onclick={regenerate}><Icon name="refresh" size={14} /> {t('channelEditor.jetzt_neu_planen')}</button>
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
                <button class="btn sm" onclick={async () => { const p = await get('/api/presets/seasonal'); ch.config.seasons = p.seasons }}>
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

        <section class="card" id="sec-order">
          <div class="section-title"><Icon name="shuffle" /><h2>{t('channelEditor.ablauf')}</h2></div>
          <div class="stack">
            <div>
              <span class="label">{t('channelEditor.reihenfolge')}</span>
              <Segmented bind:value={ch.config.order} options={[
                { value: 'shuffle', label: t('channelEditor.order.shuffle'), icon: 'shuffle' },
                { value: 'fixed', label: t('channelEditor.order.fixed'), icon: 'order' },
              ]} />
              <p class="hint">
                {#if ch.config.order === 'fixed'} {t('channelEditor.titel_laufen_immer_in_derselben')} {:else} {t('channelEditor.alle_titel_kommen_einmal_dran')} {/if}
              </p>
            </div>
            {#if ch.config.order === 'fixed' && ch.kind !== 'manual'}
              <div class="field narrow">
                <label class="label" for="sort">{t('channelEditor.sortieren_nach')}</label>
                <select id="sort" class="input" bind:value={ch.config.sortBy}>
                  <option value="">{t('channelEditor.titel')}</option>
                  <option value="year">{t('channelEditor.erscheinungsjahr')}</option>
                  <option value="release">{t('channelEditor.erscheinungsdatum')}</option>
                  <option value="added">{t('channelEditor.zuletzt_hinzugefuegt')}</option>
                </select>
              </div>
            {/if}
            {#if ch.config.order === 'shuffle'}
              <Toggle bind:checked={ch.config.keepEpisodeOrder} label={t('channelEditor.serien_in_episodenreihenfolge')}
                hint={t('channelEditor.zwischen_filmen_und_serien_wird')} />
              <div>
                <span class="label">{t('channelEditor.wiederholungen_vermeiden_fuer')}</span>
                <Segmented bind:value={ch.config.noRepeatHours} size="sm" options={repeatOptions} />
              </div>
            {/if}
          </div>
        </section>

        <section class="card" id="sec-breaks">
          <div class="section-title"><Icon name="clock" /><h2>{t('channelEditor.sendezeiten_pausen')}</h2></div>
          <div class="stack">
            {#if fillerOnly}
              <p class="hint">{t('channelEditor.dieser_sender_hat_keine_eigenen')}</p>
            {:else}
            <div>
              <span class="label">{t('channelEditor.titel_beginnen')}</span>
              <Segmented bind:value={ch.config.align} size="sm" options={alignOptions} />
              <p class="hint">
                {#if ch.config.align} {t('channelEditor.jeder_titel_startet_zur_naechsten')} {:else} {t('channelEditor.titel_laufen_direkt_nacheinander_ohne')} {/if}
              </p>
            </div>
            <div>
              <span class="label">{t('channelEditor.clips_nach_jedem_titel')}</span>
              <Segmented bind:value={ch.config.fillerBetween} size="sm" options={[0, 1, 2, 3].map((n) => ({ value: n, label: n ? String(n) : t('channelEditor.fillerNone') }))} />
              <p class="hint">{t('channelEditor.wie_werbepausen_nur_mit_trailern')}</p>
            </div>
            {/if}
            {#if fillerOnly || ch.config.align || ch.config.fillerBetween}
              <div class="breaks">
                <Toggle checked={!!ch.config.filler} onchange={toggleFiller} label={t('channelEditor.trailer_oder_clips_zeigen')}
                  hint={t('channelEditor.kinotrailer_ueber_plex_oder_eigene')} />
                {#if ch.config.filler}
                  <Segmented value={ch.config.filler.type} onchange={setFillerType} size="sm" options={[
                    { value: 'trailers', label: t('channelEditor.filler.trailers'), icon: 'film' },
                    { value: 'rules', label: t('channelEditor.filler.rules'), icon: 'filter' }, { value: 'manual', label: t('channelEditor.filler.manual'), icon: 'list' },
                  ]} />
                  {#key ch.config.filler}
                    <SourceEditor source={ch.config.filler} channel={ch} />
                  {/key}
                  {#if ch.config.filler.type === 'trailers' && effectiveTranscode === 'copy'}
                    <p class="hint">{t('channelEditor.trailer_sind_h_264_stockt')}</p>
                  {/if}
                {/if}
                {#if ch.config.align && !fillerOnly}
                  <Toggle bind:checked={ch.config.previews} label={t('channelEditor.programmvorschau')}
                    hint={t('channelEditor.am_ende_laengerer_pausen_laeuft')} />
                {/if}
                {#if ch.config.align}
                  <div class="card-preview">
                    <span class="label">{t('channelEditor.so_sieht_die_programmtafel_aus')}</span>
                    {#if ch.id}
                      <img src="/api/channels/{ch.id}/card?v={cardVersion}" alt={t('channelEditor.programmtafel')} />
                      <button class="btn ghost sm" onclick={() => cardVersion++}><Icon name="refresh" size={14} /> {t('channelEditor.aktualisieren')}</button>
                    {:else}
                      <p class="hint">{t('channelEditor.die_vorschau_erscheint_nach_dem')}</p>
                    {/if}
                  </div>
                {/if}
              </div>
            {/if}
          </div>
        </section>

        <section class="card" id="sec-logo">
          <div class="section-title"><Icon name="sparkles" /><h2>{t('channelEditor.logo')}</h2></div>
          {#if ch.kind === 'seasonal'}
            <p class="muted small mb">{t('channelEditor.gilt_ausserhalb_der_saisons_und')}</p>
          {/if}
          <LogoPicker name={ch.name} config={ch.config} seasonal={ch.kind === 'seasonal'} number={Number(ch.number) || 0} bind:color={ch.color} hint={logoHint}
            customLogo={logoUrl} posterSource={activeSource ? $state.snapshot(activeSource) : null} onupload={uploadLogo} ondelete={deleteLogo} />
        </section>

        <section class="card" id="sec-tech">
          <div class="section-title"><Icon name="cpu" /><h2>{t('channelEditor.technik')}</h2></div>
          <div class="modes">
            <label class="mode" class:active={ch.config.transcode === 'default'}>
              <input type="radio" name="tc" value="default" bind:group={ch.config.transcode} />
              <div class="row"><b class="grow">{t('channelEditor.standard')}</b>
                <span class="cpu" title={t('channelEditor.cpu_last')}>{#each [1, 2, 3] as n}<i class:on={n <= global.cpu} class:hot={global.cpu === 3}></i>{/each}</span>
              </div>
              <span class="small muted">{t('channelEditor.wie_in_den_einstellungen_festgelegt', [global.title])}</span>
            </label>
            {#each transcodeModes as m}
              <label class="mode" class:active={ch.config.transcode === m.value}>
                <input type="radio" name="tc" value={m.value} bind:group={ch.config.transcode} />
                <div class="row"><b class="grow">{m.title}</b>
                  <span class="cpu" title={t('channelEditor.cpu_last')}>{#each [1, 2, 3] as n}<i class:on={n <= m.cpu} class:hot={m.cpu === 3}></i>{/each}</span>
                </div>
                <span class="small muted">{m.text}</span>
              </label>
            {/each}
          </div>
          <div class="wm">
            <Toggle checked={ch.config.watermark.enabled} onchange={toggleWatermark} label={t('channelEditor.senderlogo_im_bild_einblenden')}
              hint={t('channelEditor.wie_im_echten_fernsehen_benoetigt')} />
            {#if ch.config.watermark.enabled}
              {#if effectiveTranscode !== 'full'}
                <div class="alert warn small"><Icon name="alert" size={15} /> {t('channelEditor.wird_nur_im_modus_vollstaendig')}</div>
              {/if}
              <div class="wm-grid">
                <div class="screen" style="--c: {ch.color || 'var(--accent)'}">
                  <img class="wm-logo" style={wmStyle} alt=""
                    src={currentLogo} />
                </div>
                <div class="stack">
                  <div>
                    <span class="label">{t('channelEditor.position')}</span>
                    <Segmented bind:value={ch.config.watermark.position} size="sm" options={[
                      { value: 'tl', label: '↖' }, { value: 'tr', label: '↗' }, { value: 'bl', label: '↙' }, { value: 'br', label: '↘' },
                    ]} />
                  </div>
                  <div>
                    <span class="label">{t('channelEditor.logoFarbe')}</span>
                    <Segmented bind:value={ch.config.watermark.tone} size="sm" options={[
                      { value: 'color', label: t('channelEditor.logoFarbe.color') }, { value: 'gray', label: t('channelEditor.logoFarbe.gray') },
                      { value: 'white', label: t('channelEditor.logoFarbe.white') },
                    ]} />
                  </div>
                  <label class="field">
                    <span class="label">{t('channelEditor.groesse', [ch.config.watermark.size])}</span>
                    <input type="range" min="6" max="30" bind:value={ch.config.watermark.size} />
                  </label>
                  <label class="field">
                    <span class="label">{t('channelEditor.deckkraft', [ch.config.watermark.opacity])}</span>
                    <input type="range" min="20" max="100" step="5" bind:value={ch.config.watermark.opacity} />
                  </label>
                </div>
              </div>
            {/if}
          </div>
          <div class="wm">
            <div>
              <span class="label">{t('channelEditor.abspann')}</span>
              <Segmented bind:value={ch.config.credits} size="sm" options={[
                { value: 'default', label: app.meta?.defaultCredits === 'squeeze' ? t('credits.defaultSqueeze') : t('credits.defaultOff') },
                { value: 'off', label: t('credits.off') }, { value: 'squeeze', label: t('credits.squeeze') },
              ]} />
              <p class="hint">{t('channelEditor.das_bild_schrumpft_beim_abspann')}</p>
            </div>
            {#if effectiveCredits === 'squeeze'}
              {#if effectiveTranscode !== 'full'}
                <div class="alert warn small"><Icon name="alert" size={15} /> {t('channelEditor.wirkt_nur_im_modus_vollstaendig')}</div>
              {/if}
              <div class="card-preview">
                {#if ch.id}
                  <img src="/api/channels/{ch.id}/credits?v={creditsVersion}" alt={t('channelEditor.infotafel_beim_abspann')} />
                  <button class="btn ghost sm" onclick={() => creditsVersion++}><Icon name="refresh" size={14} /> {t('channelEditor.aktualisieren')}</button>
                {:else}
                  <p class="hint">{t('channelEditor.die_vorschau_erscheint_nach_dem_2')}</p>
                {/if}
              </div>
            {/if}
          </div>
        </section>

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
        <div class="card sticky">
          <div class="logo-preview" style="--c: {ch.color || 'var(--accent)'}">
            <img src={currentLogo} alt="" />
          </div>
          <h3 class="mt">{t('channelEditor.vorschau')} {#if ch.kind === 'seasonal'}
              <span class="muted small">{previewSource ? t('channelEditor.previewSelection') : currentSeason ? t('channelEditor.seasonRunning', [currentSeason.emoji || '', currentSeason.name]) : t('channelEditor.restOfYear')}</span>
            {/if}
          </h3>
          {#if previewLoading && !preview}
            <div class="row mt"><span class="spinner"></span></div>
          {:else if preview}
            <div class="stats" class:loading={previewLoading}>
              <div><b>{preview.count}</b><span>{t('channelEditor.titleCount')}</span></div>
              <div><b>{fmtDuration(preview.durationMs)}</b><span>{t('channelEditor.programm')}</span></div>
              {#if preview.shows}<div><b>{preview.shows}</b><span>{t('channelEditor.serien')}</span></div>{/if}
            </div>
            {#if ch.kind === 'ai'}
              <p class="tiny muted">{t('channelEditor.aus_diesen_titeln_waehlt_die')}</p>
            {/if}
            {#if preview.count === 0}
              <div class="alert warn small"><Icon name="alert" size={15} /> {t('channelEditor.keine_treffer_passe_die_bedingungen')}</div>
            {/if}
            <div class="posters">
              {#each preview.items.slice(0, 18) as m (m.key)}
                <img src={m.thumb} alt={m.title} title="{m.title}{m.year ? ` (${m.year})` : ''}" loading="lazy" />
              {/each}
            </div>
          {/if}

          {#if ch.id && upcoming.length}
            <h3 class="mt">{t('channelEditor.als_naechstes')}</h3>
            <ul class="upcoming">
              {#each upcoming as p}
                <li>
                  <span class="time">{fmtTime(p.start)}<small>{fmtDay(p.start)}</small></span>
                  <span class="grow ellipsis">
                    {p.media.title}
                    {#if p.media.subtitle}<span class="muted tiny block ellipsis">{p.media.subtitle}</span>{/if}
                    {#if p.note}<span class="note tiny block ellipsis" title={p.note}>💡 {p.note}</span>{/if}
                  </span>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
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
  section[id] { scroll-margin-top: 1rem; }
  .savebar { position: sticky; bottom: 1rem; margin-top: 1rem; display: flex; align-items: center; gap: 0.6rem; padding: 0.7rem 1rem; border-radius: var(--radius-sm); background: var(--surface-2); border: 1px solid var(--accent); box-shadow: var(--shadow); z-index: 20; }
  .mb { margin-bottom: 1rem; }
  .mt { margin-top: 1rem; }
  .basics { display: grid; grid-template-columns: 1fr 110px; gap: 1rem; }
  .input.big { font-size: 1.15rem; font-weight: 700; padding: 0.7rem 0.9rem; }
  .narrow { max-width: 260px; }
  .fallback { margin-top: 1.5rem; padding-top: 1.25rem; border-top: 1px solid var(--border); }
  .modes { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 0.6rem; }
  .mode { display: flex; flex-direction: column; gap: 0.35rem; padding: 0.9rem; border-radius: var(--radius-sm); border: 2px solid var(--border); background: var(--bg-2); cursor: pointer; }
  .mode.active { border-color: var(--accent); }
  .mode input { display: none; }
  .cpu { display: inline-flex; gap: 3px; }
  .cpu i { width: 6px; height: 12px; border-radius: 2px; background: var(--surface-3); }
  .cpu i.on { background: var(--green); }
  .cpu i.on.hot { background: var(--red); }
  .danger-zone { border-color: rgba(239, 68, 68, 0.25); }
  .breaks { display: flex; flex-direction: column; gap: 0.9rem; padding: 1rem; background: var(--bg-2); border: 1px solid var(--border); border-radius: var(--radius-sm); }
  .card-preview { display: flex; flex-direction: column; gap: 0.4rem; align-items: flex-start; }
  .card-preview img { width: 100%; max-width: 520px; aspect-ratio: 16 / 9; border-radius: var(--radius-sm); border: 1px solid var(--border); background: var(--surface-3); }
  .wm { margin-top: 1.25rem; padding-top: 1.25rem; border-top: 1px solid var(--border); display: flex; flex-direction: column; gap: 0.9rem; }
  .wm-grid { display: grid; grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr); gap: 1.25rem; align-items: start; }
  .screen { position: relative; aspect-ratio: 16 / 9; border-radius: var(--radius-sm); overflow: hidden; border: 1px solid var(--border);
    background: linear-gradient(160deg, #3b4a6b, #1d2433 45%, #4a3328), radial-gradient(circle at 30% 70%, rgba(255,255,255,0.08), transparent 50%); }
  .screen::after { content: 'Filmszene'; position: absolute; inset: 0; display: grid; place-items: center; color: rgba(255,255,255,0.18); font-weight: 800; letter-spacing: 0.2em; text-transform: uppercase; font-size: 0.8rem; }
  .wm-logo { position: absolute; z-index: 1; height: auto; transition: all 0.2s; }
  input[type='range'] { accent-color: var(--accent); width: 100%; }
  @media (max-width: 640px) { .wm-grid { grid-template-columns: 1fr; } }
  .sticky { position: sticky; top: 1rem; }
  .logo-preview { aspect-ratio: 16 / 9; border-radius: var(--radius-sm); display: grid; place-items: center; overflow: hidden;
    background: radial-gradient(circle at 50% 35%, color-mix(in srgb, var(--c) 22%, #23252f), #121318 75%); }
  .logo-preview img { width: 92%; height: 92%; object-fit: contain; }
  .stats { display: flex; gap: 0.5rem; margin: 0.6rem 0; transition: opacity 0.2s; }
  .stats.loading { opacity: 0.5; }
  .stats div { flex: 1; display: flex; flex-direction: column; background: var(--bg-2); border-radius: var(--radius-sm); padding: 0.5rem 0.6rem; }
  .stats b { font-size: 1.05rem; }
  .stats span { font-size: 0.72rem; color: var(--muted); }
  .posters { display: grid; grid-template-columns: repeat(6, 1fr); gap: 4px; margin-top: 0.6rem; }
  .posters img { width: 100%; aspect-ratio: 2 / 3; object-fit: cover; border-radius: 4px; background: var(--surface-3); }
  .upcoming { list-style: none; padding: 0; margin: 0.5rem 0 0; display: flex; flex-direction: column; gap: 0.5rem; max-height: 360px; overflow: auto; }
  .upcoming li { display: flex; gap: 0.7rem; font-size: 0.88rem; }
  .time { font-weight: 700; color: var(--accent-2); width: 3.4rem; flex-shrink: 0; display: flex; flex-direction: column; font-size: 0.85rem; }
  .time small { color: var(--muted); font-weight: 500; font-size: 0.7rem; }
  .block { display: block; }
  .note { color: #c4b5fd; }
  @media (max-width: 1100px) {
    .layout { grid-template-columns: 1fr; }
    .side { order: -1; }
    .sticky { position: static; }
    .posters { grid-template-columns: repeat(9, 1fr); }
  }
</style>
