<script>
  import { onMount } from 'svelte'
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  import { get, post } from '../lib/api.js'
  import { app } from '../lib/state.svelte.js'

  // Adds Flimmerkiste as tuner and guide to Jellyfin or Emby with one click.
  let status = $state(null)
  let busy = $state(false)
  let error = $state('')

  onMount(async () => {
    status = await get('/api/livetv').catch((e) => ((error = e.message), null))
  })

  async function setUp() {
    busy = true
    error = ''
    try {
      const r = await post('/api/livetv/setup')
      status = { ...status, state: r.state }
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }
  const done = $derived(status?.state?.tuner && status?.state?.guide)
</script>

<div class="stack">
  {#if status?.supported}
    <p class="small">{t('livetv.explain', [status.tuner])}</p>
    {#if done}
      <div class="row ok"><Icon name="check" /> {t('livetv.done')}</div>
    {/if}
    <div class="row">
      <button class="btn {done ? '' : 'primary'}" onclick={setUp} disabled={busy}>
        {#if busy}<span class="spinner"></span>{:else}<Icon name="radio" size={16} />{/if} {done ? t('livetv.again') : t('livetv.setUp')}
      </button>
    </div>
    {#if app.meta?.platform === 'emby' || app.status?.platform === 'emby'}<p class="hint">{t('login.embyPremiere')}</p>{/if}
  {/if}
  {#if error}<div class="alert error small">{error}</div>{/if}
</div>

<style>
  .ok { color: var(--green); }
</style>
