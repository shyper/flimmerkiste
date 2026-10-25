<script>
  import { onMount, onDestroy } from 'svelte'
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../../components/Icon.svelte'
  import { get, post } from '../../lib/api.js'
  import { link } from '../../lib/router.svelte.js'
  import { toast } from '../../lib/state.svelte.js'
  import { fmtAgo } from '../../lib/format.js'

  let { s } = $props()
  let sync = $state(null)
  let timer
  let destroyed = false

  // load shows the sync status and asks again while a sync runs
  async function load() {
    clearTimeout(timer)
    const st = await get('/api/library/status').catch(() => null)
    if (destroyed) return
    sync = st
    if (sync?.running) timer = setTimeout(load, 2000)
  }
  onMount(load)
  onDestroy(() => {
    destroyed = true
    clearTimeout(timer)
  })

  async function syncNow() {
    try {
      await post('/api/library/sync')
      toast(t('settings.server.syncStarted'))
      clearTimeout(timer)
      timer = setTimeout(load, 500)
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  const intervals = [0, 1, 3, 6, 12, 24]
  const intervalLabel = (h) => (h === 0 ? t('settings.server.manual') : h === 24 ? t('settings.server.daily') : t('settings.server.everyHours', [h]))
</script>

<div class="stack">
  <div class="row"><Icon name="check" class="ok" /> <span>{t('settings.server.connected')} <b>{s.serverName}</b></span></div>
  <div class="field">
    <label class="label" for="surl">{t('settings.server.address')}</label>
    <input id="surl" class="input" bind:value={s.serverUrl} disabled={!!s.plexUrlOverride} />
    {#if s.plexUrlOverride}<span class="hint">{t('settings.server.addressFixed')}</span>{/if}
  </div>
  <div class="field">
    <label class="label" for="sync">{t('settings.server.syncEvery')}</label>
    <div class="row wrap">
      <select id="sync" class="input auto" bind:value={s.syncInterval}>
        {#each intervals.includes(s.syncInterval) ? intervals : [...intervals, s.syncInterval].sort((a, b) => a - b) as h}
          <option value={h}>{intervalLabel(h)}</option>
        {/each}
      </select>
      <button class="btn sm" onclick={syncNow} disabled={sync?.running}>
        {#if sync?.running}<span class="spinner"></span>{:else}<Icon name="refresh" size={14} />{/if} {t('settings.server.syncNow')}
      </button>
    </div>
    {#if sync}
      <span class="hint">
        {#if sync.running}{sync.message}{:else if sync.error}<span class="err">{sync.error}</span>{:else if sync.finished}{t('settings.server.lastSync', [fmtAgo(sync.finished)])}{/if}
      </span>
    {/if}
  </div>
  <p class="small muted">{t('settings.server.folders')} <a href="/library" use:link>{t('settings.server.foldersLink')}</a></p>
</div>

<style>
  .auto { width: auto; }
  .err { color: var(--red); }
  .stack :global(.ok) { color: var(--green); }
</style>
