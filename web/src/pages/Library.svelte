<script>
  import { t } from '../lib/i18n.svelte.js'
  import { onMount, onDestroy } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import Help from '../components/Help.svelte'
  import Toggle from '../components/Toggle.svelte'
  import { get, post, put } from '../lib/api.js'
  import { app, toast } from '../lib/state.svelte.js'
  import { fmtAgo, fmtNumber } from '../lib/format.js'

  let data = $state(null)
  // Folders mounted in the container, per library: Plex folder -> local folder
  let paths = $state({})
  let checks = $state({})
  let savingPaths = $state(null)
  let timer

  async function load() {
    data = await get('/api/libraries')
    for (const l of data.libraries) {
      // keep what is being typed while the sync status is polled
      paths[l.id] ??= Object.fromEntries((l.locations || []).map((x) => [x.plex, x.local || '']))
    }
    if (!data.status.running && timer) {
      clearInterval(timer)
      timer = null
      get('/api/library/facets').then((f) => (app.facets = f))
      get('/api/meta').then((m) => (app.meta = m))
    }
  }
  onMount(async () => {
    await load()
    if (data.status.running) poll()
  })
  onDestroy(() => clearInterval(timer))

  function poll() {
    clearInterval(timer)
    timer = setInterval(load, 1500)
  }

  async function sync() {
    await post('/api/library/sync')
    toast(t('library.syncStarted'))
    poll()
    load()
  }

  async function savePaths(l) {
    savingPaths = l.id
    try {
      const res = await put(`/api/libraries/${l.id}/paths`, { paths: $state.snapshot(paths[l.id]) })
      checks[l.id] = res
      if (!res.checked) toast(t('library.saved'))
      else if (res.found === res.checked) toast(t('library.readDirectly', [l.title]))
    } catch (e) {
      checks[l.id] = { error: e.message }
    } finally {
      savingPaths = null
    }
  }

  async function setEnabled(l, enabled) {
    await put(`/api/libraries/${l.id}`, { enabled })
    toast(enabled ? t('library.used', [l.title]) : t('library.ignored', [l.title]))
    if (enabled) poll()
    load()
  }
</script>

<div class="page">
  <div class="page-head">
    <div class="grow">
      <h1>{t('library.bibliothek')}</h1>
      <p>{t('library.waehle_welche_plex_bibliotheken_fuer')}</p>
    </div>
    <button class="btn primary" onclick={sync} disabled={data?.status.running}>
      {#if data?.status.running}<span class="spinner"></span>{:else}<Icon name="refresh" size={16} />{/if} {t('library.jetzt_synchronisieren')} </button>
  </div>

  {#if data}
    <div class="status card">
      {#if data.status.running}
        <span class="spinner"></span>
      {:else if data.status.error}
        <Icon name="alert" class="bad" />
      {:else}
        <Icon name="check" class="good" />
      {/if}
      <div class="grow">
        <b>{data.status.message || t('library.ready')}</b>
        {#if data.status.error}<div class="small err">{data.status.error}</div>{/if}
        {#if data.status.finished}
          <div class="tiny muted"> {t('library.zuletzt_abgeschlossen', [fmtAgo(data.status.finished)])} {#if data.status.added || data.status.removed}· <span class="good-t">{t('library.neu', [data.status.added])}</span> · <span class="bad-t">{t('library.entfernt', [data.status.removed])}</span> {t('library.betroffene_sender_wurden_neu_geplant')}{/if}
          </div>
        {/if}
      </div>
    </div>

    <div class="libs">
      {#each data.libraries as l (l.id)}
        <div class="lib card">
          <span class="ic"><Icon name={l.type === 'movie' ? 'film' : 'tv'} size={22} /></span>
          <div class="grow">
            <h3>{l.title}</h3>
            <p class="small muted">{l.type === 'movie' ? t('library.movieInfo', [fmtNumber(l.items), fmtAgo(l.syncedAt)]) : t('library.showInfo', [fmtNumber(l.items), fmtAgo(l.syncedAt)])}</p>
          </div>
          <Toggle checked={l.enabled} onchange={(v) => setEnabled(l, v)} />
          {#if data.container && l.locations?.length}
            <details class="direct">
            <summary class="small">{t('library.directAccess')} <span class="muted">· {t('library.mapped', [l.locations.filter((loc) => paths[l.id][loc.plex]).length, l.locations.length])}</span></summary>
            <p class="hint">{t('library.directHint')}</p>
            <Help>{t('library.directMore')}</Help>
            <div class="paths">
              {#each l.locations as loc (loc.plex)}
                <label class="path">
                  <span class="plex" title={t('library.ordner_in_plex')}><Icon name="folder" size={14} /> {loc.plex}</span>
                  <span class="arrow">→</span>
                  <input class="input" placeholder={t('library.lokaler_pfad_im_container_z')} bind:value={paths[l.id][loc.plex]} />
                </label>
              {/each}
              <div class="row">
                <button class="btn sm" onclick={() => savePaths(l)} disabled={savingPaths === l.id}>
                  {#if savingPaths === l.id}<span class="spinner"></span>{:else}<Icon name="check" size={14} />{/if} {t('library.speichern_pruefen')} </button>
                {#if checks[l.id]?.error}
                  <span class="small err">{checks[l.id].error}</span>
                {:else if checks[l.id]?.checked}
                  <span class="small" class:good-t={checks[l.id].found === checks[l.id].checked} class:err={checks[l.id].found < checks[l.id].checked}> {t('library.von_testdateien_gefunden', [checks[l.id].found, checks[l.id].checked])}{#if checks[l.id].missing} {t('library.nicht_lesbar', [checks[l.id].missing])}{/if}
                  </span>
                {/if}
              </div>
            </div>
            </details>
          {/if}
        </div>
      {:else}
        <div class="empty card">{t('library.noch_keine_bibliotheken_gefunden_starte')}</div>
      {/each}
    </div>
    <p class="hint">{t('library.syncHint')}</p>
    <Help>{t('library.syncMore')}{#if !data.container} {t('library.liegen_die_dateien_auf_diesem')}{/if}</Help>
  {/if}
</div>

<style>
  .status { display: flex; align-items: center; gap: 0.9rem; margin-bottom: 1rem; }
  .status :global(.good) { color: var(--green); }
  .status :global(.bad) { color: var(--red); }
  .err { color: #fca5a5; }
  .good-t { color: #86efac; }
  .bad-t { color: #fca5a5; }
  .libs { display: flex; flex-direction: column; gap: 0.6rem; margin-bottom: 0.75rem; }
  .lib { display: flex; align-items: center; gap: 1rem; padding: 1rem 1.25rem; flex-wrap: wrap; }
  .direct { flex-basis: 100%; padding-top: 0.6rem; border-top: 1px solid var(--border); }
  .direct summary { cursor: pointer; }
  .paths { display: flex; flex-direction: column; gap: 0.5rem; padding-top: 0.6rem; }
  .path { display: grid; grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr); gap: 0.6rem; align-items: center; }
  .plex { display: inline-flex; align-items: center; gap: 0.35rem; font-family: var(--mono, monospace); font-size: 0.82rem; color: var(--text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .arrow { color: var(--muted); }
  @media (max-width: 640px) { .path { grid-template-columns: 1fr; } .arrow { display: none; } }
  .ic { width: 44px; height: 44px; border-radius: 12px; display: grid; place-items: center; background: rgba(229, 160, 13, 0.12); color: var(--accent); }
</style>
