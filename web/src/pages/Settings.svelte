<script>
  import { onMount, onDestroy } from 'svelte'
  import { t, i18n } from '../lib/i18n.svelte.js'
  import Icon from '../components/Icon.svelte'
  import { get, put } from '../lib/api.js'
  import { toast } from '../lib/state.svelte.js'
  import { link, navigate, route, leaveGuard } from '../lib/router.svelte.js'
  import Server from './settings/Server.svelte'
  import LiveTV from './settings/LiveTV.svelte'
  import Playback from './settings/Playback.svelte'
  import Presentation from './settings/Presentation.svelte'
  import Trailers from './settings/Trailers.svelte'
  import Hardware from './settings/Hardware.svelte'
  import AI from './settings/AI.svelte'
  import Users from './settings/Users.svelte'
  import Mcp from './settings/Mcp.svelte'
  import General from './settings/General.svelte'
  import NotFound from '../components/NotFound.svelte'
  import LoadError from '../components/LoadError.svelte'

  let { onchange } = $props()

  const groups = [
    { id: 'connection', sections: [['server', 'layers', Server], ['livetv', 'radio', LiveTV]] },
    { id: 'programme', sections: [['playback', 'play', Playback], ['presentation', 'sparkles', Presentation], ['trailers', 'film', Trailers]] },
    { id: 'advanced', sections: [['hardware', 'cpu', Hardware], ['ai', 'wand', AI], ['users', 'user', Users], ['mcp', 'key', Mcp], ['general', 'settings', General]] },
  ]
  const sections = groups.flatMap((g) => g.sections)
  // /settings opens the first section; an unknown one is a page that does not exist
  const current = $derived(sections.find(([id]) => route.path === `/settings/${id}`) || (route.path === '/settings' ? sections[0] : null))

  // What the server stores; anything else in the answer is information.
  const editable = ['language', 'friendlyName', 'epgPrefix', 'tunerCount', 'normalizeRes', 'audioLanguages', 'syncInterval',
    'streamBuffer', 'hwAccel', 'hwDecode', 'defaultTranscode', 'trailerHubs', 'trailerCaption', 'cardMusic', 'credits',
    'allowSharedUsers', 'aiProvider', 'aiModel', 'aiBaseUrl', 'aiCandidates', 'aiKey', 'serverUrl']
  const pick = (v) => JSON.stringify(editable.map((k) => v?.[k]))

  let s = $state(null)
  let saved = $state('')
  let saving = $state(false)
  let loadError = $state('')
  const dirty = $derived(!!s && pick(s) !== saved)

  async function load() {
    loadError = ''
    try {
      s = await get('/api/settings')
      saved = pick(s)
    } catch (e) {
      loadError = e.message
    }
  }
  onMount(load)

  // persisted: a section saved some values itself (a test, a refresh), so
  // they no longer count as unsaved, and discarding does not seem to undo them
  function persisted(values) {
    const base = JSON.parse(saved || '[]')
    editable.forEach((k, i) => {
      if (k in values) base[i] = values[k] ?? null
    })
    saved = JSON.stringify(base)
  }

  function onBeforeUnload(e) {
    if (dirty) e.preventDefault()
  }
  onMount(() => {
    window.addEventListener('beforeunload', onBeforeUnload)
    // within the settings changes survive switching sections; leaving asks first
    leaveGuard.hold(guard)
  })
  const guard = (to) => !dirty || to.startsWith('/settings') || confirm(t('settings.leaveConfirm'))
  onDestroy(() => {
    window.removeEventListener('beforeunload', onBeforeUnload)
    leaveGuard.release(guard)
  })

  $effect(() => {
    if (!current) return (document.title = `${t('app.seite_nicht_gefunden')} · Flimmerkiste`)
    document.title = `${t(`settings.${current[0]}.title`)} · ${t('settings.title')} · Flimmerkiste`
  })

  async function save() {
    saving = true
    try {
      const reload = s.language !== i18n.lang
      const data = await put('/api/settings', $state.snapshot(s))
      if (data) s = data
      saved = pick(s)
      // texts computed once in scripts follow only after a reload
      if (reload) return location.reload()
      toast(t('settings.saved'), 'success')
      onchange?.()
    } catch (e) {
      toast(e.message, 'error')
    } finally {
      saving = false
    }
  }

  async function discard() {
    try {
      s = await get('/api/settings')
      saved = pick(s)
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  function pickSection(e) {
    navigate(`/settings/${e.currentTarget.value}`)
  }
</script>

{#if !current}
  <NotFound />
{:else}
<div class="page settings">
  <div class="page-head">
    <div class="grow"><h1>{t('settings.title')}</h1></div>
  </div>

  <div class="layout">
    <nav class="side" aria-label={t('settings.title')}>
      {#each groups as g}
        <div class="group">{t(`settings.group.${g.id}`)}</div>
        {#each g.sections as [id, icon]}
          <a href="/settings/{id}" use:link class:active={current[0] === id} aria-current={current[0] === id ? 'page' : undefined}>
            <Icon name={icon} size={16} /> {t(`settings.${id}.title`)}
          </a>
        {/each}
      {/each}
    </nav>
    <select class="input mobile-nav" value={current[0]} onchange={pickSection} aria-label={t('settings.title')}>
      {#each groups as g}
        <optgroup label={t(`settings.group.${g.id}`)}>
          {#each g.sections as [id]}<option value={id}>{t(`settings.${id}.title`)}</option>{/each}
        </optgroup>
      {/each}
    </select>

    <section class="card content">
      <div class="section-title"><Icon name={current[1]} /><h2>{t(`settings.${current[0]}.title`)}</h2></div>
      <p class="small muted intro">{t(`settings.${current[0]}.intro`)}</p>
      {#if s}
        {@const Section = current[2]}
        <Section {s} {persisted} />
      {:else if loadError}
        <LoadError message={loadError} retry={load} />
      {:else}
        <div class="skeleton" style="height: 240px"></div>
      {/if}
    </section>
  </div>

  {#if dirty}
    <div class="savebar" role="region" aria-label={t('settings.unsaved')}>
      <span class="grow"><Icon name="alert" size={16} /> {t('settings.unsaved')}</span>
      <button class="btn" onclick={discard} disabled={saving}>{t('settings.discard')}</button>
      <button class="btn primary" onclick={save} disabled={saving}>
        {#if saving}<span class="spinner"></span>{:else}<Icon name="check" size={16} />{/if} {t('settings.save')}
      </button>
    </div>
  {/if}
</div>
{/if}

<style>
  .layout { display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 1.25rem; align-items: start; }
  .side { position: sticky; top: 1rem; display: flex; flex-direction: column; gap: 2px; }
  .group { font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted); margin: 0.9rem 0.6rem 0.3rem; }
  .group:first-child { margin-top: 0; }
  .side a { display: flex; align-items: center; gap: 0.6rem; padding: 0.5rem 0.65rem; border-radius: var(--radius-sm); color: var(--text-2); text-decoration: none; font-size: 0.92rem; }
  .side a:hover { background: var(--surface); color: var(--text); }
  .side a.active { background: var(--surface-2); color: var(--accent-2); font-weight: 600; }
  .mobile-nav { display: none; }
  .content { min-height: 300px; }
  .intro { margin: -0.4rem 0 1rem; }
  .savebar { position: sticky; bottom: 1rem; margin-top: 1rem; display: flex; align-items: center; gap: 0.6rem; padding: 0.7rem 1rem; border-radius: var(--radius-sm); background: var(--surface-2); border: 1px solid var(--accent); box-shadow: var(--shadow); z-index: 20; }
  .savebar :global(.icon) { vertical-align: -3px; color: var(--accent-2); }
  @media (max-width: 760px) {
    .layout { grid-template-columns: 1fr; }
    .side { display: none; }
    .mobile-nav { display: block; }
  }
</style>
