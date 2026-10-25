<script>
  import { t } from '../lib/i18n.svelte.js'
  import { onMount, onDestroy } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import Menu from '../components/Menu.svelte'
  import Modal from '../components/Modal.svelte'
  import LoadError from '../components/LoadError.svelte'
  import { app, isAdmin, toast } from '../lib/state.svelte.js'
  import { get, post, del } from '../lib/api.js'
  import { link, navigate } from '../lib/router.svelte.js'
  import { fmtTime, fmtDuration, kindLabels, kindIcons } from '../lib/format.js'
  import { poll } from '../lib/poll.js'

  let channels = $state(null)
  let sessions = $state([])
  let loadError = $state('')
  let now = $state(Date.now())
  let poller, clock
  let confirmReplan = $state(false)

  async function load() {
    try {
      const [c, s] = await Promise.all([get('/api/channels'), get('/api/sessions')])
      channels = c
      sessions = s
      loadError = ''
    } catch (e) {
      loadError = e.message
    }
  }
  onMount(() => {
    // Poll often while a channel is being planned, otherwise rarely.
    poller = poll(load, () => (channels?.some((c) => c.planning) ? 3000 : 30000))
    clock = setInterval(() => (now = Date.now()), 10000)
  })
  onDestroy(() => { poller?.stop(); clearInterval(clock) })

  async function replanAll() {
    confirmReplan = false
    try {
      const r = await post('/api/channels/regenerate')
      toast(t('channels.replanning', [r.channels]))
    } catch (e) {
      toast(e.message, 'error')
    }
    poller?.now()
  }

  async function dismissError(e, c) {
    e.stopPropagation()
    try {
      await del(`/api/channels/${c.id}/error`)
      c.error = ''
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  const pct = (p) => Math.min(100, Math.max(0, ((now - p.start) / (p.stop - p.start)) * 100))
  const watching = (id) => sessions.filter((s) => s.channelId === id).length
</script>

<div class="page">
  <div class="page-head">
    <div class="grow">
      <h1>{t('channels.deine_sender')}</h1>
      <p>{channels ? t('channels.count', [channels.length]) : t('channels.loading')}{sessions.length ? t('channels.watching', [sessions.length]) : ''}</p>
    </div>
    {#if isAdmin()}
      <a class="btn primary" href="/channels/new" use:link><Icon name="plus" size={16} /> {t('channels.neuer_sender')}</a>
      {#if channels?.length}
        <Menu label={t('channels.more')} items={[{ label: t('channels.alle_neu_planen'), icon: 'refresh', onclick: () => (confirmReplan = true) }]} />
      {/if}
    {/if}
  </div>

  {#if !app.meta?.linked}
    <div class="alert warn"><Icon name="alert" size={16} /> {t('channels.es_ist_noch_kein_plex')}</div>
  {/if}

  {#if channels === null && loadError}
    <LoadError message={loadError} retry={() => poller?.now()} />
  {:else if channels === null}
    <div class="grid">{#each Array(3) as _}<div class="skeleton sk"></div>{/each}</div>
  {:else if !channels.length}
    <div class="empty-state card">
      <div class="tv-art"><Icon name="tv" size={48} /></div>
      <h2>{t('channels.noch_keine_sender')}</h2>
      <p class="muted">{t('channels.erstelle_deinen_ersten_sender_z')}</p>
      {#if isAdmin()}<a class="btn primary lg" href="/channels/new" use:link><Icon name="plus" /> {t('channels.ersten_sender_erstellen')}</a>{/if}
    </div>
  {:else}
    <div class="grid">
      {#each channels as c (c.id)}
        <!-- the name's button covers the card (::after), the close control sits above it -->
        <div class="ch" class:disabled={!c.enabled}>
          <div class="logo" style="--c: {c.color || 'var(--accent)'}">
            <img src={c.logoUrl} alt={c.name} loading="lazy" />
            <span class="num">{c.number}</span>
            {#if watching(c.id)}<span class="chip live onair">{t('channels.live', [watching(c.id)])}</span>{/if}
          </div>
          <div class="info">
            <div class="row">
              <h3 class="grow ellipsis"><button class="open" onclick={() => navigate(isAdmin() ? `/channels/${c.id}` : '/guide')}>{c.name}</button></h3>
              {#if c.planning}<span class="chip violet planning" role="status"><span class="spinner"></span> {t('channels.planning')}</span>{/if}
              <span class="chip"><Icon name={kindIcons[c.kind]} size={12} /> {kindLabels[c.kind]}</span>
            </div>
            {#if c.error}
              <div class="err tiny">
                <Icon name="alert" size={13} /> <span class="grow">{c.error}</span>
                {#if isAdmin()}
                  <button class="dismiss" title={t('channels.meldung_schliessen')} aria-label={t('channels.meldung_schliessen')}
                    onclick={(e) => dismissError(e, c)}><Icon name="x" size={13} /></button>
                {/if}
              </div>
            {/if}
            {#if !c.enabled}
              <p class="muted small">{t('channels.deaktiviert')}</p>
            {:else if c.now}
              <div class="now">
                {#if c.now.source}<span class="tiny season">{c.now.source}</span>{/if}
                <div class="ellipsis"><b>{c.now.media.title}</b></div>
                {#if c.now.media.subtitle}<div class="ellipsis small muted">{c.now.media.subtitle}</div>{/if}
                <div class="progress"><div style="width: {pct(c.now)}%"></div></div>
                <div class="row tiny muted">
                  <span>{fmtTime(c.now.start)}</span><span class="spacer"></span><span>{t('channels.noch', [fmtDuration(c.now.stop - now)])}</span>
                </div>
              </div>
              {#if c.next}
                <div class="next small ellipsis"><span class="muted">{fmtTime(c.next.start)}</span> {c.next.media.title}</div>
              {/if}
            {:else}
              <p class="muted small row"><span class="spinner"></span> {t('channels.programm_wird_erstellt')}</p>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<Modal bind:open={confirmReplan} title={t('channels.alle_sender_neu_planen')}>
  <p>{t('channels.das_zukuenftige_programm_aller_aktiven')}</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (confirmReplan = false)}>{t('channels.abbrechen')}</button>
    <button class="btn primary" onclick={replanAll}><Icon name="refresh" size={16} /> {t('channels.neu_planen')}</button>
  {/snippet}
</Modal>

<style>
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(290px, 1fr)); gap: 1rem; }
  .sk { height: 320px; border-radius: var(--radius); }
  .ch { position: relative; text-align: left; display: flex; flex-direction: column; border-radius: 16px; overflow: hidden; border: 1px solid var(--border); background: var(--surface); color: var(--text); cursor: pointer; padding: 0; transition: transform 0.15s, border-color 0.15s, box-shadow 0.15s; }
  .ch:has(.open:focus-visible) { outline: 2px solid var(--accent); outline-offset: 2px; }
  .open { all: unset; cursor: pointer; }
  .open::after { content: ''; position: absolute; inset: 0; }
  .ch:hover { transform: translateY(-3px); border-color: var(--border-strong); box-shadow: var(--shadow); }
  .ch.disabled { opacity: 0.55; }
  .logo { position: relative; aspect-ratio: 16 / 9; display: grid; place-items: center; overflow: hidden;
    background: radial-gradient(circle at 50% 35%, color-mix(in srgb, var(--c) 22%, #23252f), #121318 75%); }
  .logo img { width: 92%; height: 92%; object-fit: contain; filter: drop-shadow(0 6px 14px rgba(0,0,0,0.45)); }
  .num { position: absolute; top: 0.6rem; left: 0.6rem; background: rgba(0, 0, 0, 0.55); backdrop-filter: blur(6px); padding: 0.1rem 0.55rem; border-radius: 6px; font-weight: 800; font-size: 0.85rem; }
  .onair { position: absolute; top: 0.6rem; right: 0.6rem; animation: pulse 2s infinite; }
  @keyframes pulse { 50% { opacity: 0.7; } }
  .info { padding: 0.9rem 1rem 1rem; display: flex; flex-direction: column; gap: 0.5rem; flex: 1; }
  .now { display: flex; flex-direction: column; gap: 0.3rem; }
  .season { color: var(--accent-2); font-weight: 700; }
  .next { padding-top: 0.4rem; border-top: 1px dashed var(--border); }
  .planning .spinner { width: 10px; height: 10px; border-width: 1.5px; }
  .err { color: #fca5a5; display: flex; gap: 0.3rem; align-items: flex-start; }
  .dismiss { position: relative; z-index: 1; border: 0; background: none; color: inherit; flex-shrink: 0; display: grid; place-items: center; padding: 0.15rem; margin: -0.15rem; border-radius: 4px; opacity: 0.7; cursor: pointer; }
  .dismiss:hover, .dismiss:focus-visible { opacity: 1; background: rgba(239, 68, 68, 0.18); }
  .empty-state { text-align: center; padding: 3rem 1.5rem; display: flex; flex-direction: column; align-items: center; gap: 0.9rem; }
  .tv-art { width: 90px; height: 90px; border-radius: 24px; display: grid; place-items: center; background: rgba(229, 160, 13, 0.12); color: var(--accent); }
</style>
