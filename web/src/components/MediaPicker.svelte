<script>
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  import Segmented from './Segmented.svelte'
  import { get } from '../lib/api.js'
  import { debounce } from '../lib/format.js'
  import { app } from '../lib/state.svelte.js'

  let { items = $bindable([]) } = $props()

  let q = $state('')
  let tab = $state('movie')
  let results = $state({ movies: [], shows: [], moreMovies: false, moreShows: false })
  let loading = $state(false)
  let loadingMore = $state(false)
  let searchID = 0 // answers to an older search are dropped
  let page = $state(0) // counts every new answer, so the end-of-list marker is observed afresh
  let expanded = $state(null)
  let episodes = $state([])
  let dragIndex = $state(-1)

  const search = debounce(async () => {
    const id = ++searchID
    loading = true
    try {
      const res = await get(`/api/library/search?q=${encodeURIComponent(q)}`)
      if (id === searchID) {
        results = res
        page++
      }
    } finally {
      if (id === searchID) loading = false
    }
  }, 250)
  search()

  // Endless scroll: the server answers in pages; the next one loads when the end of the list comes into view.
  async function loadMore() {
    const movie = tab === 'movie'
    if (loadingMore || loading || !(movie ? results.moreMovies : results.moreShows)) return
    const id = searchID
    loadingMore = true
    try {
      const offset = movie ? results.movies.length : results.shows.length
      const res = await get(`/api/library/search?q=${encodeURIComponent(q)}&type=${movie ? 'movie' : 'show'}&offset=${offset}`)
      if (id !== searchID) return
      if (movie) {
        results.movies.push(...res.movies)
        results.moreMovies = res.moreMovies
      } else {
        results.shows.push(...res.shows)
        results.moreShows = res.moreShows
      }
      page++
    } finally {
      loadingMore = false
    }
  }

  function sentinel(node) {
    if (typeof IntersectionObserver === 'undefined') return
    const io = new IntersectionObserver((entries) => entries[0].isIntersecting && loadMore(), { root: node.parentElement, rootMargin: '200px' })
    io.observe(node)
    return { destroy: () => io.disconnect() }
  }

  const has = (key, type, season) => items.some((i) => i.key === key && i.type === type && (i.season || 0) === (season || 0))

  function add(entry) {
    if (!has(entry.key, entry.type, entry.season)) items = [...items, entry]
  }

  async function toggleShow(show) {
    if (expanded === show.key) {
      expanded = null
      return
    }
    expanded = show.key
    episodes = []
    episodes = await get(`/api/library/show/${encodeURIComponent(show.key)}`)
  }

  function move(from, to) {
    if (to < 0 || to >= items.length || from === to) return
    const next = [...items]
    const [it] = next.splice(from, 1)
    next.splice(to, 0, it)
    items = next
  }

  // Entries without a year go last; within a year the order stays as it is.
  function sortByYear() {
    items = [...items].sort((a, b) => (a.year || 9999) - (b.year || 9999))
  }

  const isMissing = (it) => app.missing.includes(`${it.type}|${it.key}|${it.season || 0}`)

  const typeLabel = {
    get movie() { return t('mediaPicker.type.movie') },
    get show() { return t('mediaPicker.type.show') },
    get season() { return t('mediaPicker.type.season') },
    get episode() { return t('mediaPicker.type.episode') },
  }
</script>

<div class="picker">
  <div class="search-col">
    <div class="row">
      <div class="search grow">
        <Icon name="search" size={16} />
        <input class="input" placeholder={t('mediaPicker.filme_oder_serien_suchen')} bind:value={q} oninput={search} />
      </div>
      <Segmented bind:value={tab} size="sm" options={[{ value: 'movie', label: t('mediaPicker.movies') }, { value: 'show', label: t('mediaPicker.shows') }]} />
    </div>
    <div class="results">
      {#if loading && !results.movies.length && !results.shows.length}
        <div class="empty small"><span class="spinner"></span></div>
      {:else if tab === 'movie'}
        {#each results.movies as m (m.key)}
          <div class="res">
            <img src={m.thumb} alt="" loading="lazy" />
            <div class="grow">
              <div class="ellipsis">{m.title}</div>
              <div class="muted tiny">{m.year || ''}</div>
            </div>
            <button class="btn sm" disabled={has(m.key, 'movie')} onclick={() => add({ key: m.key, type: 'movie', title: m.title, subtitle: String(m.year || ''), year: m.year })}>
              <Icon name={has(m.key, 'movie') ? 'check' : 'plus'} size={14} />
            </button>
          </div>
        {:else}
          <div class="empty small">{t('mediaPicker.keine_filme_gefunden')}</div>
        {/each}
        {#if results.moreMovies}
          {#key page}<div class="more" use:sentinel>{#if loadingMore}<span class="spinner"></span>{/if}</div>{/key}
        {/if}
      {:else}
        {#each results.shows as s (s.key)}
          <div class="res">
            <img src={s.thumb} alt="" loading="lazy" />
            <button class="grow show-btn" onclick={() => toggleShow(s)}>
              <div class="ellipsis">{s.title}</div>
              <div class="muted tiny">{t('mediaPicker.staffeln_episoden', [s.seasons.length, s.episodes])}</div>
            </button>
            <button class="btn sm" disabled={has(s.key, 'show')} title={t('mediaPicker.ganze_serie')}
              onclick={() => add({ key: s.key, type: 'show', title: s.title, subtitle: t('mediaPicker.episodes', [s.episodes]), year: s.year })}>
              <Icon name={has(s.key, 'show') ? 'check' : 'plus'} size={14} /> {t('mediaPicker.serie')} </button>
          </div>
          {#if expanded === s.key}
            <div class="expand">
              <div class="row wrap">
                {#each s.seasons as n}
                  <button class="chip pick" class:accent={has(s.key, 'season', n)}
                    onclick={() => add({ key: s.key, type: 'season', season: n, title: s.title, subtitle: n === 0 ? t('mediaPicker.specials') : t('mediaPicker.seasonN', [n]), year: s.year })}>
                    + {n === 0 ? t('mediaPicker.specials') : t('mediaPicker.seasonN', [n])}
                  </button>
                {/each}
              </div>
              <div class="eps">
                {#each episodes as e (e.key)}
                  <div class="ep">
                    <span class="mono muted">{e.subtitle}</span>
                    <span class="grow ellipsis">{e.title}</span>
                    <button class="btn ghost icon sm" disabled={has(e.key, 'episode')}
                      onclick={() => add({ key: e.key, type: 'episode', title: s.title, subtitle: `${e.subtitle} · ${e.title}`, year: s.year })}>
                      <Icon name={has(e.key, 'episode') ? 'check' : 'plus'} size={14} />
                    </button>
                  </div>
                {:else}
                  <span class="spinner"></span>
                {/each}
              </div>
            </div>
          {/if}
        {:else}
          <div class="empty small">{t('mediaPicker.keine_serien_gefunden')}</div>
        {/each}
        {#if results.moreShows}
          {#key page}<div class="more" use:sentinel>{#if loadingMore}<span class="spinner"></span>{/if}</div>{/key}
        {/if}
      {/if}
    </div>
  </div>

  <div class="selected-col">
    <div class="row">
      <h4 class="grow">{t('mediaPicker.programmablauf')} <span class="muted small">({items.length})</span></h4>
      {#if items.length > 1}
        <button class="btn ghost sm" onclick={sortByYear} title={t('mediaPicker.z_b_fuer_filmreihen')}><Icon name="order" size={14} /> {t('mediaPicker.nach_jahr')}</button>
      {/if}
    </div>
    {#if !items.length}
      <div class="drop-empty">
        <Icon name="film" size={28} />
        <p>{t('mediaPicker.fuege_links_filme_serien_staffeln')}</p>
        <p class="tiny muted">{t('mediaPicker.die_reihenfolge_kannst_du_per')}</p>
      </div>
    {/if}
    <ol class="selected">
      {#each items as it, i (it.key + it.type + (it.season || ''))}
        <li draggable="true" class:dragging={dragIndex === i} class:missing={isMissing(it)}
          ondragstart={() => (dragIndex = i)} ondragend={() => (dragIndex = -1)}
          ondragover={(e) => { e.preventDefault(); if (dragIndex >= 0 && dragIndex !== i) { move(dragIndex, i); dragIndex = i } }}>
          <span class="grip"><Icon name="grip" size={14} /></span>
          <span class="num">{i + 1}</span>
          <div class="grow">
            <div class="ellipsis">{it.title}</div>
            <div class="muted tiny ellipsis">{typeLabel[it.type]}{it.subtitle ? ' · ' + it.subtitle : ''}</div>
            {#if isMissing(it)}<span class="chip red tiny">{t('mediaPicker.nicht_mehr_in_plex_wird')}</span>{/if}
          </div>
          <button class="btn ghost icon sm" onclick={() => move(i, i - 1)} disabled={i === 0} aria-label={t('mediaPicker.nach_oben')}><Icon name="up" size={14} /></button>
          <button class="btn ghost icon sm" onclick={() => move(i, i + 1)} disabled={i === items.length - 1} aria-label={t('mediaPicker.nach_unten')}><Icon name="down" size={14} /></button>
          <button class="btn ghost icon sm" onclick={() => (items = items.filter((_, j) => j !== i))} aria-label={t('mediaPicker.entfernen')}><Icon name="x" size={14} /></button>
        </li>
      {/each}
    </ol>
  </div>
</div>

<style>
  /* minmax(0, …) and min-width: 0 let long titles shorten instead of widening the columns */
  .picker { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 1rem; }
  .search-col, .selected-col, .selected .grow, .res .grow { min-width: 0; }
  .search { position: relative; display: flex; align-items: center; }
  .search :global(.icon) { position: absolute; left: 0.7rem; color: var(--muted); }
  .search .input { padding-left: 2.2rem; }
  .more { min-height: 2rem; display: grid; place-items: center; flex-shrink: 0; }
  .results { margin-top: 0.6rem; max-height: 440px; overflow: auto; display: flex; flex-direction: column; gap: 2px; }
  .res { display: flex; align-items: center; gap: 0.7rem; padding: 0.35rem; border-radius: var(--radius-sm); }
  .res:hover { background: var(--surface-2); }
  .res img { width: 34px; height: 50px; object-fit: cover; border-radius: 4px; background: var(--surface-3); flex-shrink: 0; }
  .show-btn { border: 0; background: none; color: inherit; text-align: left; font: inherit; cursor: pointer; padding: 0; }
  .expand { margin: 0 0 0.5rem 2.8rem; padding: 0.6rem; background: var(--bg-2); border-radius: var(--radius-sm); display: flex; flex-direction: column; gap: 0.5rem; }
  .chip.pick { cursor: pointer; border: 0; font: inherit; font-size: 0.78rem; font-weight: 600; }
  .eps { max-height: 220px; overflow: auto; }
  .ep { display: flex; align-items: center; gap: 0.6rem; font-size: 0.85rem; padding: 0.1rem 0; }
  .selected-col { background: var(--bg-2); border: 1px solid var(--border); border-radius: var(--radius); padding: 0.9rem; min-height: 200px; }
  .drop-empty { text-align: center; color: var(--muted); padding: 2rem 1rem; display: flex; flex-direction: column; align-items: center; gap: 0.5rem; font-size: 0.9rem; }
  .selected { list-style: none; margin: 0.6rem 0 0; padding: 0; display: flex; flex-direction: column; gap: 4px; max-height: 470px; overflow: auto; }
  .selected li { display: flex; align-items: center; gap: 0.5rem; padding: 0.4rem 0.5rem; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-sm); cursor: grab; }
  .selected li.dragging { opacity: 0.5; border-style: dashed; }
  .selected li.missing { border-color: rgba(239, 68, 68, 0.45); background: rgba(239, 68, 68, 0.06); }
  .selected li.missing .num { color: var(--red); }
  .grip { color: var(--muted); display: flex; }
  .num { font-weight: 800; color: var(--accent); font-size: 0.8rem; width: 1.6rem; text-align: right; }
  @media (max-width: 900px) { .picker { grid-template-columns: 1fr; } }
</style>
