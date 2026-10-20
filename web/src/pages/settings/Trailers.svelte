<script>
  import { onMount, onDestroy } from 'svelte'
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../../components/Icon.svelte'
  import Help from '../../components/Help.svelte'
  import Segmented from '../../components/Segmented.svelte'
  import { get, put, post } from '../../lib/api.js'
  import { app, toast } from '../../lib/state.svelte.js'
  import { fmtAgo } from '../../lib/format.js'

  let { s } = $props()
  // The cinema & streaming lists come from Plex Discover.
  const plex = $derived((app.meta?.platform || 'plex') === 'plex')
  let trailers = $state(null)
  let hubs = $state(null)
  let hubsError = $state('')
  let timer

  async function load() {
    trailers = await get('/api/trailers').catch(() => null)
    clearTimeout(timer)
    if (trailers?.discover?.running) timer = setTimeout(load, 3000)
  }
  onMount(load)
  onDestroy(() => clearTimeout(timer))

  async function loadHubs() {
    hubsError = ''
    try {
      hubs = await get('/api/trailers/hubs')
    } catch (e) {
      hubsError = e.message
    }
  }

  function toggleHub(id, on) {
    s.trailerHubs = on ? [...s.trailerHubs, id] : s.trailerHubs.filter((h) => h !== id)
  }

  async function refresh() {
    try {
      await put('/api/settings', { trailerHubs: $state.snapshot(s.trailerHubs) })
      await post('/api/trailers/refresh')
      toast(t('settings.trailers.fetching'))
      setTimeout(load, 1500)
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  async function refreshSlates() {
    try {
      await put('/api/settings', { tmdbKey: s.tmdbKey, tmdbRegion: s.tmdbRegion, slateSeconds: s.slateSeconds })
      await post('/api/slates/refresh')
      toast(t('settings.trailers.slatesFetching'))
      setTimeout(load, 1500)
    } catch (e) {
      toast(e.message, 'error')
    }
  }
  $effect(() => {
    if (trailers?.slates?.running || trailers?.info?.weather?.running || trailers?.info?.news?.running) {
      clearTimeout(timer)
      timer = setTimeout(load, 3000)
    }
  })

  // Weather and news slates (Jellyfin/Emby)
  let placeQuery = $state('')
  let places = $state(null)
  let placeError = $state('')
  async function searchPlaces() {
    placeError = ''
    try {
      places = await get('/api/info/places?q=' + encodeURIComponent(placeQuery.trim()))
      if (!places.length) placeError = t('settings.trailers.placeNone')
    } catch (e) {
      placeError = e.message
    }
  }
  function choosePlace(p) {
    s.weatherPlace = p
    places = null
    placeQuery = ''
  }
  async function refreshInfo() {
    try {
      await put('/api/settings', { weatherPlace: $state.snapshot(s.weatherPlace), newsFeeds: s.newsFeeds })
      await post('/api/info/refresh')
      toast(t('settings.trailers.infoFetching'))
      setTimeout(load, 2500)
    } catch (e) {
      toast(e.message, 'error')
    }
  }
  const info = $derived(trailers?.info)
  const feedExample = 'https://www.tagesschau.de/index~rss2.xml'

  // Trailer counts per origin in the preferred languages (slates have no language).
  const counts = $derived.by(() => {
    const out = { library: { titles: 0, count: 0 }, discover: { titles: 0, count: 0 }, tmdb: { titles: 0, count: 0 }, folder: { titles: 0, count: 0 } }
    const langs = new Set(trailers?.languages || [])
    const alias = { deu: ['de', 'ger'], ger: ['de', 'deu'], eng: ['en'], fra: ['fr', 'fre'] }
    const wanted = (l) => !langs.size || langs.has(l) || [...langs].some((p) => alias[p]?.includes(l))
    for (const st of trailers?.stats || []) {
      if (!out[st.origin] || (st.origin !== 'tmdb' && !wanted(st.language))) continue
      out[st.origin].titles += st.titles
      out[st.origin].count += st.count
    }
    return out
  })
</script>

<div class="stack">
  <p class="small muted">{t('settings.trailers.intro')}</p>
  {#if trailers}
    <div class="tstats">
      <div><b>{counts.library.titles}</b><span>{t('settings.trailers.libraryCount')}</span></div>
      {#if plex}<div><b>{counts.discover.titles}</b><span>{t('settings.trailers.discoverCount')}</span></div>
      {:else}<div><b>{counts.tmdb.titles}</b><span>{t('settings.trailers.slatesCount')}</span></div>{/if}
      {#if trailers.folder}<div><b>{counts.folder.titles}</b><span>{t('settings.trailers.folderCount')}</span></div>{/if}
    </div>
    {#if trailers.library}<div class="tiny muted">{trailers.library}</div>{/if}
    {#if trailers.folder}
      <div class="tiny muted">{t('settings.trailers.folderStatus', [trailers.folder.dir])}{#if trailers.folder.last} · {t('settings.trailers.last', [fmtAgo(trailers.folder.last)])}{/if}</div>
      {#if trailers.folder.error}<div class="alert error small">{trailers.folder.error}</div>{/if}
    {/if}
  {/if}
  {#if plex}
  <div class="field">
    <span class="label">{t('settings.trailers.lists')}</span>
    {#if s.trailerHubs.length && !hubs}
      <div class="chips">{#each s.trailerHubs as h}<span class="chip">{h.replace('home.', '')}</span>{/each}</div>
    {/if}
    {#if hubs}
      <div class="hubs">
        {#each hubs as h (h.hubIdentifier)}
          <label class="hub"><input type="checkbox" checked={s.trailerHubs.includes(h.hubIdentifier)}
            onchange={(e) => toggleHub(h.hubIdentifier, e.currentTarget.checked)} /> {h.title}</label>
        {/each}
      </div>
    {:else}
      <div class="row"><button class="btn sm" onclick={loadHubs}><Icon name="list" size={14} /> {t('settings.trailers.chooseLists')}</button></div>
    {/if}
    {#if hubsError}<div class="alert error small">{hubsError}</div>{/if}
    <span class="hint">{t('settings.trailers.listsHint')}</span>
    <Help>{t('settings.trailers.listsMore')}</Help>
  </div>
  {#if s.trailerHubs.length}
    <div class="row">
      <button class="btn sm" onclick={refresh} disabled={trailers?.discover?.running}>
        {#if trailers?.discover?.running}<span class="spinner"></span> {t('settings.trailers.running')}{:else}<Icon name="refresh" size={14} /> {t('settings.trailers.fetchNow')}{/if}
      </button>
      {#if trailers?.discover?.last && !trailers.discover.running}
        <span class="tiny muted">{t('settings.trailers.last', [fmtAgo(trailers.discover.last)])}{#if trailers.discover.result?.titles} {t('settings.trailers.result', [trailers.discover.result.titles, trailers.discover.result.trailers])}{/if}</span>
      {/if}
    </div>
    {#if trailers?.discover?.error}<div class="alert error small">{trailers.discover.error}</div>{/if}
  {/if}
  {:else}
  <div class="field">
    <span class="label">{t('settings.trailers.slates')}</span>
    <span class="hint">{t('settings.trailers.slatesHint')}</span>
    <Help>{t('settings.trailers.slatesMore')}</Help>
  </div>
  <div class="field">
    <label class="label" for="tmdbKey">{t('settings.trailers.tmdbKey')}</label>
    <input id="tmdbKey" class="input" type="password" bind:value={s.tmdbKey} placeholder={t('settings.trailers.tmdbKeyPlaceholder')} autocomplete="off" />
    <span class="hint">{t('settings.trailers.tmdbKeyHint')} <a href="https://www.themoviedb.org/settings/api" target="_blank" rel="noopener">themoviedb.org</a></span>
  </div>
  <div class="grid2">
    <div class="field">
      <label class="label" for="tmdbRegion">{t('settings.trailers.region')}</label>
      <input id="tmdbRegion" class="input" bind:value={s.tmdbRegion} maxlength="2" placeholder={s.tmdbRegionAuto} />
      <span class="hint">{t('settings.trailers.regionHint')}</span>
    </div>
    <div class="field">
      <span class="label">{t('settings.trailers.slateLength')}</span>
      <Segmented bind:value={s.slateSeconds} size="sm" options={[15, 20, 30].map((n) => ({ value: n, label: t('settings.trailers.seconds', [n]) }))} />
    </div>
  </div>
  {#if s.tmdbKey}
    <div class="row">
      <button class="btn sm" onclick={refreshSlates} disabled={trailers?.slates?.running}>
        {#if trailers?.slates?.running}<span class="spinner"></span> {t('settings.trailers.running')}{:else}<Icon name="refresh" size={14} /> {t('settings.trailers.fetchNow')}{/if}
      </button>
      {#if trailers?.slates?.last && !trailers.slates.running}
        <span class="tiny muted">{t('settings.trailers.last', [fmtAgo(trailers.slates.last)])}{#if trailers.slates.result?.titles} {t('settings.trailers.slatesResult', [trailers.slates.result.titles])}{/if}</span>
      {/if}
    </div>
    {#if trailers?.slates?.error}<div class="alert error small">{trailers.slates.error}</div>{/if}
  {/if}
  <p class="tiny muted attribution">{t('settings.trailers.tmdbNotice')}</p>
  {/if}

  <div class="field sep">
    <span class="label">{t('settings.trailers.info')}</span>
    <span class="hint">{t('settings.trailers.infoHint')}</span>
    <Help>{t('settings.trailers.infoMore')}</Help>
  </div>
  <div class="field">
    <span class="label">{t('settings.trailers.place')}</span>
    {#if s.weatherPlace}
      <div class="row place"><Icon name="check" size={15} /> <span class="grow">{s.weatherPlace.name}{#if s.weatherPlace.region}<span class="muted"> · {s.weatherPlace.region}</span>{/if}</span>
        <button class="btn ghost sm" onclick={() => (s.weatherPlace = null)}>{t('settings.trailers.placeRemove')}</button></div>
    {:else}
      <form class="row" onsubmit={(e) => (e.preventDefault(), searchPlaces())}>
        <input class="input grow" bind:value={placeQuery} placeholder={t('settings.trailers.placePlaceholder')} aria-label={t('settings.trailers.place')} />
        <button class="btn sm" disabled={placeQuery.trim().length < 2}><Icon name="search" size={14} /> {t('settings.trailers.placeSearch')}</button>
      </form>
      {#if places?.length}
        <div class="places">
          {#each places as p}
            <button class="place-opt" onclick={() => choosePlace(p)}>{p.name} <span class="muted">· {p.region}</span></button>
          {/each}
        </div>
      {/if}
      {#if placeError}<div class="alert error small">{placeError}</div>{/if}
      <span class="hint">{t('settings.trailers.placeHint')}</span>
    {/if}
  </div>
  <div class="field">
    <label class="label" for="newsFeeds">{t('settings.trailers.feeds')}</label>
    <textarea id="newsFeeds" class="input" rows="3" bind:value={s.newsFeeds} placeholder={feedExample}></textarea>
    <span class="hint">{t('settings.trailers.feedsHint')}</span>
  </div>
  {#if s.weatherPlace || s.newsFeeds?.trim()}
    <div class="row">
      <button class="btn sm" onclick={refreshInfo} disabled={info?.weather?.running || info?.news?.running}>
        {#if info?.weather?.running || info?.news?.running}<span class="spinner"></span> {t('settings.trailers.running')}{:else}<Icon name="refresh" size={14} /> {t('settings.trailers.fetchNow')}{/if}
      </button>
      {#if info?.weather?.last && s.weatherPlace}
        <span class="tiny muted">{t('settings.trailers.weatherStatus', [fmtAgo(info.weather.last)])}</span>
      {/if}
      {#if info?.news?.last && s.newsFeeds?.trim()}
        <span class="tiny muted">{t('settings.trailers.newsStatus', [fmtAgo(info.news.last), info.news.count])}</span>
      {/if}
    </div>
    {#if info?.weather?.error && s.weatherPlace}<div class="alert error small">{info.weather.error}</div>{/if}
    {#if info?.news?.error && s.newsFeeds?.trim()}<div class="alert error small">{info.news.error}</div>{/if}
  {/if}
</div>

<style>
  .tstats { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 0.5rem; max-width: 780px; }
  .tstats div { display: flex; flex-direction: column; padding: 0.6rem 0.75rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  .tstats b { font-size: 1.3rem; }
  .tstats span { font-size: 0.78rem; color: var(--muted); }
  .hubs { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 0.3rem 0.75rem; max-height: 260px; overflow: auto; padding: 0.5rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  .hub { display: flex; align-items: center; gap: 0.45rem; font-size: 0.85rem; cursor: pointer; }
  .grid2 { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 0.75rem; }
  .attribution { margin-top: 0.25rem; }
  .sep { border-top: 1px solid var(--border); padding-top: 1rem; margin-top: 0.5rem; }
  .place { align-items: center; color: var(--green); }
  .place .muted { color: var(--muted); }
  .places { display: flex; flex-direction: column; gap: 0.2rem; padding: 0.4rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  .place-opt { text-align: left; padding: 0.35rem 0.5rem; border-radius: var(--radius-sm); background: none; border: 0; color: var(--text); cursor: pointer; font: inherit; }
  .place-opt:hover { background: var(--bg-3, rgba(255, 255, 255, 0.06)); }
  textarea.input { resize: vertical; font-family: var(--mono, monospace); font-size: 0.82rem; }
  .chips { display: flex; flex-wrap: wrap; gap: 0.3rem; }
</style>
