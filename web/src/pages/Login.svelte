<script>
  import { t } from '../lib/i18n.svelte.js'
  import { onDestroy } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import Segmented from '../components/Segmented.svelte'
  import { app } from '../lib/state.svelte.js'
  import { get, post } from '../lib/api.js'
  import { navigate } from '../lib/router.svelte.js'

  let { onlogin } = $props()
  let busy = $state(false)
  let error = $state('')
  let authUrl = $state('')
  let timer

  // Before the instance is claimed the owner picks the media server.
  const claimed = $derived(!!app.status?.claimed)
  let choice = $state('plex')
  const platform = $derived(claimed ? app.status?.platform || 'plex' : choice)
  const names = { plex: 'Plex', jellyfin: 'Jellyfin', emby: 'Emby' }

  // Jellyfin/Emby: address (first sign-in only), then name and password
  let url = $state('')
  let server = $state(null) // { name, product, version } once the address answered
  let username = $state('')
  let password = $state('')

  async function connect() {
    error = ''
    busy = true
    try {
      server = await post('/api/setup/connect', { url })
      url = server.url
      if (server.platform !== choice) choice = server.platform
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  async function passwordLogin() {
    error = ''
    busy = true
    try {
      await post('/api/auth/login', { url: claimed ? undefined : url, username, password })
      password = ''
      if (!claimed) navigate('/setup')
      onlogin()
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  async function plexLogin() {
    error = ''
    busy = true
    // Open the window synchronously (popup blockers), then point it at plex.tv.
    const popup = window.open('', 'plexauth', 'width=600,height=720')
    try {
      const pin = await post('/api/auth/pin')
      authUrl = pin.authUrl
      if (popup) popup.location.href = pin.authUrl
      const started = Date.now()
      timer = setInterval(async () => {
        if (Date.now() - started > 10 * 60 * 1000) {
          stop(t('login.timeout'))
          return
        }
        try {
          const res = await get(`/api/auth/pin/${pin.id}`)
          if (res.status === 'ok') {
            stop()
            popup?.close()
            onlogin()
          } else if (res.status === 'denied') {
            stop(res.error || t('login.denied'))
            popup?.close()
          }
        } catch (e) {
          stop(e.message)
        }
      }, 1500)
    } catch (e) {
      popup?.close()
      stop(e.message)
    }
  }

  function stop(msg = '') {
    clearInterval(timer)
    busy = false
    error = msg
  }
  onDestroy(() => clearInterval(timer))

  function pick(p) {
    choice = p
    server = null
    error = ''
  }
</script>

<div class="login">
  <div class="glow"></div>
  <div class="panel">
    <img src="/favicon.svg" alt="" width="64" height="64" />
    <h1>{t('login.brandStart')}<span>{t('login.brandEnd')}</span></h1>
    <p class="lead">{t('login.lead')}</p>

    {#if !claimed}
      <div class="choose">
        <span class="label">{t('login.choosePlatform')}</span>
        <Segmented value={choice} onchange={pick} options={[{ value: 'plex', label: 'Plex' }, { value: 'jellyfin', label: 'Jellyfin' }, { value: 'emby', label: 'Emby' }]} />
      </div>
      {#if choice === 'emby'}<div class="alert info small">{t('login.embyPremiere')}</div>{/if}
    {/if}

    {#if platform === 'plex'}
      {#if !claimed}
        <div class="alert info small">{t('login.erste_anmeldung_das_plex_konto')}</div>
      {/if}
      <button class="btn primary lg wide" onclick={plexLogin} disabled={busy}>
        {#if busy}<span class="spinner"></span> {t('login.warte_auf_plex')}{:else}{t('login.mit_plex_anmelden')}{/if}
      </button>
      {#if busy && authUrl}
        <p class="tiny muted">{t('login.kein_fenster_aufgegangen')} <a href={authUrl} target="_blank" rel="noopener">{t('login.hier_anmelden')}</a></p>
      {/if}
      <p class="tiny muted foot">{t('login.zugriff_haben_nur_der_eigentuemer')}</p>
    {:else if !claimed && !server}
      <form class="form" onsubmit={(e) => (e.preventDefault(), connect())}>
        <label class="label" for="url">{t('login.serverAddress', [names[platform]])}</label>
        <input id="url" class="input" bind:value={url} placeholder="http://192.168.1.20:8096" autocomplete="url" required />
        <button class="btn primary lg wide" disabled={busy || !url.trim()}>
          {#if busy}<span class="spinner"></span>{:else}<Icon name="right" size={16} />{/if} {t('login.connect')}
        </button>
      </form>
    {:else}
      <form class="form" onsubmit={(e) => (e.preventDefault(), passwordLogin())}>
        {#if server}
          <div class="row small server"><Icon name="check" size={15} /> <span class="grow">{t('login.found', [server.product, server.version, server.name])}</span>
            <button type="button" class="btn ghost sm" onclick={() => (server = null)}>{t('login.change')}</button></div>
          <div class="alert info small">{t('login.firstAdmin', [names[platform]])}</div>
        {:else}
          <p class="small muted">{t('login.signInAt', [names[platform], app.status?.serverName || ''])}</p>
        {/if}
        <label class="label" for="user">{t('login.username')}</label>
        <input id="user" class="input" bind:value={username} autocomplete="username" required />
        <label class="label" for="pass">{t('login.password')}</label>
        <input id="pass" class="input" type="password" bind:value={password} autocomplete="current-password" />
        <button class="btn primary lg wide" disabled={busy || !username.trim()}>
          {#if busy}<span class="spinner"></span>{/if} {t('login.signIn')}
        </button>
        <p class="tiny muted">{t('login.passwordNotStored')}</p>
      </form>
    {/if}
    {#if error}<div class="alert error small">{error}</div>{/if}
  </div>
</div>

<style>
  .login { min-height: 100vh; display: grid; place-items: center; padding: 1.5rem; position: relative; overflow: hidden; }
  .glow { position: absolute; width: 700px; height: 700px; border-radius: 50%; background: radial-gradient(circle, rgba(229, 160, 13, 0.18), transparent 65%); filter: blur(20px); animation: float 12s ease-in-out infinite alternate; }
  @keyframes float { to { transform: translate(80px, -60px) scale(1.1); } }
  .panel { position: relative; width: min(440px, 100%); text-align: center; display: flex; flex-direction: column; align-items: center; gap: 1rem; padding: 2.5rem 2rem; background: rgba(23, 25, 32, 0.85); border: 1px solid var(--border); border-radius: 20px; box-shadow: var(--shadow); backdrop-filter: blur(10px); }
  h1 { font-size: 2rem; }
  h1 span { color: var(--accent); }
  .lead { color: var(--text-2); }
  .wide { width: 100%; margin-top: 0.5rem; }
  .foot { margin-top: 0.5rem; }
  .alert { text-align: left; width: 100%; }
  .choose { display: flex; flex-direction: column; align-items: center; gap: 0.4rem; }
  .form { width: 100%; display: flex; flex-direction: column; gap: 0.4rem; text-align: left; }
  .server { color: var(--green); align-items: center; }
</style>
