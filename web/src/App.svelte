<script>
  import { t, setLang, setServer } from './lib/i18n.svelte.js'
  import { onMount } from 'svelte'
  import { app, isAdmin } from './lib/state.svelte.js'
  import { route, match, link, navigate } from './lib/router.svelte.js'
  import { get, post } from './lib/api.js'
  import Icon from './components/Icon.svelte'
  import Toasts from './components/Toasts.svelte'
  import Login from './pages/Login.svelte'
  import Setup from './pages/Setup.svelte'
  import Channels from './pages/Channels.svelte'
  import NewChannel from './pages/NewChannel.svelte'
  import ChannelEditor from './pages/ChannelEditor.svelte'
  import Guide from './pages/Guide.svelte'
  import Library from './pages/Library.svelte'
  import Settings from './pages/Settings.svelte'

  let menuOpen = $state(false)

  async function refresh() {
    app.status = await get('/api/auth/status')
    setLang(app.status.language)
    setServer(app.status.platform)
    app.user = app.status.user || null
    if (app.user) {
      app.meta = await get('/api/meta')
      if (isAdmin()) get('/api/library/facets').then((f) => (app.facets = f)).catch(() => {})
    }
    app.ready = true
  }

  onMount(refresh)

  async function logout() {
    await post('/api/auth/logout')
    app.user = null
    navigate('/')
  }

  const nav = [
    { href: '/', get label() { return t('app.nav.channels') }, icon: 'tv' },
    { href: '/guide', get label() { return t('app.nav.guide') }, icon: 'guide' },
    { href: '/library', get label() { return t('app.nav.library') }, icon: 'library', admin: true },
    { href: '/settings', get label() { return t('app.nav.settings') }, icon: 'settings', admin: true },
  ]
  const activeNav = (href) => (href === '/' ? route.path === '/' || route.path.startsWith('/channels') : route.path.startsWith(href))
  const needsSetup = $derived(app.user && isAdmin() && !app.status?.serverLinked)

  // Browser tab title per page (the settings set their own).
  $effect(() => {
    if (route.path.startsWith('/settings')) return
    const n = nav.find((n) => n.href !== '/' && route.path.startsWith(n.href))
    const page = n ? n.label : route.path.startsWith('/channels') ? t('app.title.editor') : t('app.nav.channels')
    document.title = `${page} · Flimmerkiste`
  })
</script>

{#if !app.ready}
  <div class="boot"><span class="spinner"></span></div>
{:else if !app.user}
  <Login onlogin={refresh} />
{:else if needsSetup || route.path === '/setup'}
  <Setup ondone={refresh} />
{:else}
  <div class="shell">
    <aside class:open={menuOpen}>
      <a class="brand" href="/" use:link>
        <img src="/favicon.svg" alt="" width="30" height="30" />
        <span>{t('app.brandStart')}<b>{t('app.brandEnd')}</b></span>
      </a>
      <nav>
        {#each nav.filter((n) => !n.admin || isAdmin()) as n}
          <a href={n.href} use:link class:active={activeNav(n.href)} onclick={() => (menuOpen = false)}>
            <Icon name={n.icon} /> {n.label}
          </a>
        {/each}
      </nav>
      <div class="spacer"></div>
      <div class="me">
        {#if app.user.thumb}<img src={app.user.thumb} alt="" />{:else}<Icon name="user" />{/if}
        <div class="grow">
          <div class="ellipsis small"><b>{app.user.username}</b></div>
          <div class="tiny muted">{app.user.role === 'admin' ? t('role.admin') : t('role.viewer')}{app.meta?.serverName ? ' · ' + app.meta.serverName : ''}</div>
        </div>
        <button class="btn ghost icon sm" onclick={logout} title={t('app.abmelden')}><Icon name="logout" size={16} /></button>
      </div>
    </aside>
    <header class="mobile-bar">
      <a class="brand" href="/" use:link>
        <img src="/favicon.svg" alt="" width="26" height="26" />
        <span>{t('app.brandStart')}<b>{t('app.brandEnd')}</b></span>
      </a>
      <button class="btn icon" onclick={() => (menuOpen = !menuOpen)} aria-label={t('app.menue')} aria-expanded={menuOpen}><Icon name="menu" /></button>
    </header>
    {#if menuOpen}<div class="scrim" onclick={() => (menuOpen = false)} role="presentation"></div>{/if}
    <main>
      {#if route.path === '/'}
        <Channels />
      {:else if route.path === '/channels/new' && isAdmin()}
        <NewChannel />
      {:else if match('/channels/new/:kind') && isAdmin()}
        <ChannelEditor kind={match('/channels/new/:kind').kind} />
      {:else if match('/channels/:id') && isAdmin()}
        {#key route.path}<ChannelEditor id={match('/channels/:id').id} />{/key}
      {:else if route.path === '/guide'}
        <Guide />
      {:else if route.path === '/library' && isAdmin()}
        <Library />
      {:else if route.path.startsWith('/settings') && isAdmin()}
        <Settings onchange={refresh} />
      {:else}
        <div class="page empty"><h2>{t('app.seite_nicht_gefunden')}</h2><p><a href="/" use:link>{t('app.zur_uebersicht')}</a></p></div>
      {/if}
    </main>
  </div>
{/if}
<Toasts />

<style>
  .boot { min-height: 100vh; display: grid; place-items: center; }
  .shell { display: grid; grid-template-columns: 240px 1fr; min-height: 100vh; }
  aside { position: sticky; top: 0; height: 100vh; display: flex; flex-direction: column; gap: 1.5rem; padding: 1.25rem 0.9rem; border-right: 1px solid var(--border); background: rgba(13, 14, 18, 0.7); backdrop-filter: blur(12px); z-index: 50; }
  .brand { display: flex; align-items: center; gap: 0.6rem; color: var(--text); font-size: 1.1rem; padding: 0 0.5rem; text-decoration: none; }
  .brand b { color: var(--accent); }
  nav { display: flex; flex-direction: column; gap: 2px; }
  nav a { display: flex; align-items: center; gap: 0.75rem; padding: 0.6rem 0.75rem; border-radius: var(--radius-sm); color: var(--text-2); font-weight: 600; text-decoration: none; transition: background 0.15s, color 0.15s; }
  nav a:hover { background: var(--surface); color: var(--text); }
  nav a.active { background: linear-gradient(90deg, rgba(229, 160, 13, 0.16), rgba(229, 160, 13, 0.04)); color: var(--accent-2); }
  .me { display: flex; align-items: center; gap: 0.6rem; padding: 0.6rem; border-radius: var(--radius-sm); background: var(--surface); border: 1px solid var(--border); }
  .me img { width: 32px; height: 32px; border-radius: 50%; }
  main { min-width: 0; }
  .mobile-bar, .scrim { display: none; }
  @media (max-width: 860px) {
    .shell { grid-template-columns: 1fr; }
    aside { position: fixed; left: 0; width: 260px; transform: translateX(-100%); transition: transform 0.2s; }
    aside.open { transform: none; box-shadow: var(--shadow); }
    .mobile-bar { display: flex; align-items: center; justify-content: space-between; position: sticky; top: 0; z-index: 40; padding: 0.5rem 0.75rem; background: rgba(13, 14, 18, 0.85); backdrop-filter: blur(12px); border-bottom: 1px solid var(--border); }
    .mobile-bar .brand { font-size: 1rem; padding: 0; }
    .scrim { display: block; position: fixed; inset: 0; z-index: 45; background: rgba(5, 6, 10, 0.5); }
  }
</style>
