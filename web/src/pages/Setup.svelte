<script>
  import { t } from '../lib/i18n.svelte.js'
  import { onMount, onDestroy } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import TunerHelp from '../components/TunerHelp.svelte'
  import LiveTVSetup from '../components/LiveTVSetup.svelte'
  import { app, toast } from '../lib/state.svelte.js'
  import { get, post } from '../lib/api.js'
  import { navigate } from '../lib/router.svelte.js'

  let { ondone } = $props()

  let step = $state(app.status?.serverLinked ? 2 : 1)
  let servers = $state(null)
  let selected = $state('')
  let url = $state('')
  let busy = $state(false)
  let error = $state('')
  let sync = $state(null)
  let libs = $state([])
  let poll

  onMount(async () => {
    if (step === 1) {
      try {
        servers = await get('/api/setup/servers')
        if (servers.length) pick(servers[0])
      } catch (e) {
        error = e.message
        servers = []
      }
    } else {
      startPolling()
    }
  })
  onDestroy(() => clearInterval(poll))

  function pick(s) {
    selected = s.id
    url = s.suggested || s.connections[0] || ''
  }

  async function connect() {
    busy = true
    error = ''
    try {
      await post('/api/setup/server', { id: selected, url })
      step = 2
      startPolling()
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  function startPolling() {
    const load = async () => {
      const r = await get('/api/libraries')
      libs = r.libraries
      sync = r.status
    }
    load()
    poll = setInterval(load, 1500)
  }

  async function finish(target) {
    clearInterval(poll)
    await ondone()
    navigate(target)
  }
</script>

<div class="setup">
  <div class="head">
    <img src="/favicon.svg" alt="" width="44" height="44" />
    <div>
      <h1>{t('setup.willkommen_bei_flimmerkiste')}</h1>
      <p class="muted">{t('setup.in_drei_schritten_zu_deinem')}</p>
    </div>
  </div>

  <div class="progress-steps">
    {#each [t('setup.step1'), t('setup.step2'), t('setup.step3')] as label, i}
      <div class="ps" class:done={step > i + 1} class:current={step === i + 1}>
        <span class="dot">{#if step > i + 1}<Icon name="check" size={14} />{:else}{i + 1}{/if}</span>{label}
      </div>
    {/each}
  </div>

  <div class="card">
    {#if step === 1}
      <h2>{t('setup.welchen_plex_server_moechtest_du')}</h2>
      <p class="muted small sub">{t('setup.wir_haben_deine_server_ueber')}</p>
      {#if servers === null}
        <div class="row"><span class="spinner"></span> {t('setup.suche_server')}</div>
      {:else if !servers.length}
        <div class="alert warn">{t('setup.keine_eigenen_plex_server_gefunden', [error])}</div>
      {:else}
        <div class="servers">
          {#each servers as s}
            <button class="server" class:active={selected === s.id} onclick={() => pick(s)}>
              <Icon name="radio" size={22} />
              <div class="grow">
                <b>{s.name}</b>
                <div class="tiny muted">{s.suggested ? t('setup.reachableAt', [s.suggested]) : t('setup.noConnection')}</div>
              </div>
              {#if selected === s.id}<Icon name="check" />{/if}
            </button>
          {/each}
        </div>
        {@const sel = servers.find((s) => s.id === selected)}
        <div class="field">
          <label class="label" for="url">{t('setup.adresse_des_servers')}</label>
          <input id="url" class="input" list="conns" bind:value={url} placeholder="http://127.0.0.1:32400" />
          <datalist id="conns">{#each sel?.connections || [] as c}<option value={c}></option>{/each}</datalist>
          <span class="hint">{t('setup.laeuft_flimmerkiste_auf_demselben_rechne')} <code>http://127.0.0.1:32400</code> {t('setup.isRight')}</span>
        </div>
        {#if error}<div class="alert error small">{error}</div>{/if}
        <div class="row end">
          <button class="btn primary" onclick={connect} disabled={!selected || !url || busy}>
            {#if busy}<span class="spinner"></span>{/if} {t('setup.verbinden')} <Icon name="right" size={16} />
          </button>
        </div>
      {/if}
    {:else if step === 2}
      <h2>{t('setup.bibliothek_wird_eingelesen')}</h2>
      <p class="muted small sub">{t('setup.filme_und_serien_werden_einmalig')}</p>
      <div class="sync">
        {#if sync?.running}<span class="spinner"></span>{:else if sync?.error}<Icon name="alert" />{:else}<Icon name="check" class="ok" />{/if}
        <span>{sync?.message || '…'}</span>
      </div>
      {#if sync?.error}<div class="alert error small">{sync.error}</div>{/if}
      <ul class="libs">
        {#each libs as l}
          <li><Icon name={l.type === 'movie' ? 'film' : 'tv'} size={16} /> <span class="grow">{l.title}</span> <span class="muted small">{t('setup.eintraege', [l.items])}</span></li>
        {/each}
      </ul>
      <div class="row end">
        <button class="btn primary" onclick={() => (step = 3)} disabled={sync?.running}>{t('setup.weiter')} <Icon name="right" size={16} /></button>
      </div>
    {:else}
      <h2>{t('setup.tuner_in_plex_hinzufuegen')}</h2>
      <p class="muted small sub">{t('setup.plex_sieht_flimmerkiste_wie_einen')}</p>
      {#if app.status?.platform && app.status.platform !== 'plex'}<LiveTVSetup />{:else}<TunerHelp />{/if}
      <div class="row end">
        <button class="btn" onclick={() => finish('/')}>{t('setup.spaeter')}</button>
        <button class="btn primary" onclick={() => finish('/channels/new')}><Icon name="plus" size={16} /> {t('setup.ersten_sender_erstellen')}</button>
      </div>
    {/if}
  </div>
</div>

<style>
  .setup { max-width: 720px; margin: 0 auto; padding: 3rem 1.25rem; display: flex; flex-direction: column; gap: 1.5rem; }
  .head { display: flex; gap: 1rem; align-items: center; }
  .progress-steps { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .ps { display: flex; align-items: center; gap: 0.5rem; color: var(--muted); font-size: 0.88rem; font-weight: 600; flex: 1; min-width: 160px; }
  .ps .dot { width: 26px; height: 26px; border-radius: 50%; display: grid; place-items: center; background: var(--surface-2); border: 1px solid var(--border-strong); font-size: 0.8rem; }
  .ps.current { color: var(--text); }
  .ps.current .dot { background: var(--accent); color: var(--accent-ink); border-color: var(--accent); }
  .ps.done .dot { background: var(--green); color: #052e12; border-color: var(--green); }
  .card { display: flex; flex-direction: column; gap: 1rem; }
  .sub { margin-top: -0.5rem; }
  .servers { display: flex; flex-direction: column; gap: 0.5rem; }
  .server { display: flex; align-items: center; gap: 0.9rem; padding: 0.9rem 1rem; border-radius: var(--radius-sm); border: 2px solid var(--border); background: var(--bg-2); color: var(--text); font: inherit; text-align: left; cursor: pointer; }
  .server.active { border-color: var(--accent); }
  .server :global(.icon) { color: var(--accent); }
  .end { justify-content: flex-end; }
  .sync { display: flex; gap: 0.6rem; align-items: center; font-weight: 600; }
  .sync :global(.ok) { color: var(--green); }
  .libs { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.4rem; }
  .libs li { display: flex; align-items: center; gap: 0.6rem; padding: 0.55rem 0.75rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  code { color: var(--accent-2); }
</style>
