<script>
  import { t, locale } from '../lib/i18n.svelte.js'
  import { onMount } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import { get } from '../lib/api.js'
  import { fmtTime, fmtDay, fmtDuration, debounce } from '../lib/format.js'

  const pageSize = 100
  let channels = $state([])
  let entries = $state(null)
  let more = $state(false)
  let loading = $state(false)
  let error = $state('')
  let search = $state('')
  let channel = $state('')
  let day = $state('')

  function query(before) {
    const q = new URLSearchParams({ limit: String(pageSize) })
    if (search.trim()) q.set('q', search.trim())
    if (channel) q.set('channel', channel)
    if (day) q.set('day', day)
    if (before) q.set('before', String(before))
    return '/api/history?' + q
  }

  async function load(append = false) {
    loading = true
    error = ''
    try {
      const page = await get(query(append && entries?.length ? entries[entries.length - 1].start : 0))
      entries = append ? [...entries, ...page] : page
      more = page.length === pageSize
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
  const reload = debounce(() => load(), 300)

  onMount(async () => {
    channels = await get('/api/channels').catch(() => [])
    load()
  })

  // entries grouped by the day they started
  const days = $derived.by(() => {
    const out = []
    for (const e of entries || []) {
      const key = new Date(e.start).toDateString()
      if (out.at(-1)?.key !== key) out.push({ key, start: e.start, items: [] })
      out.at(-1).items.push(e)
    }
    return out
  })

  const pad = (n) => String(n).padStart(2, '0')
  const title = (e) => (e.type === 'episode' ? e.showTitle : e.title)
  const detail = (e) =>
    e.type === 'episode' ? `S${pad(e.season)}E${pad(e.episode)} · ${e.title}` : e.year ? String(e.year) : ''
  const timesHint = (e) =>
    e.previous ? t('history.timesHint', [e.times, fmtDuration(e.start - e.previous)]) : t('history.first')
</script>

<div class="page">
  <div class="page-head">
    <div class="grow">
      <h1>{t('history.title')}</h1>
      <p>{t('history.intro')}</p>
    </div>
  </div>

  <div class="filters">
    <label class="search">
      <Icon name="search" size={16} />
      <input class="input" type="search" bind:value={search} oninput={reload} placeholder={t('history.search')} aria-label={t('history.search')} />
    </label>
    <select class="input" bind:value={channel} onchange={() => load()} aria-label={t('history.channel')}>
      <option value="">{t('history.allChannels')}</option>
      {#each channels as c (c.id)}<option value={String(c.id)}>{c.name}</option>{/each}
    </select>
    <input class="input day" type="date" bind:value={day} onchange={() => load()} aria-label={t('history.day')} />
  </div>

  {#if error}<div class="alert error small">{error}</div>{/if}

  {#if entries}
    {#each days as d (d.key)}
      <h3 class="day-head">{fmtDay(d.start)} <span class="muted">{new Date(d.start).toLocaleDateString(locale())}</span></h3>
      <div class="list card">
        {#each d.items as e (e.id)}
          <div class="row">
            <span class="time">{fmtTime(e.start)}</span>
            <span class="what grow">
              <b>{title(e)}</b>{#if detail(e)} <span class="muted">· {detail(e)}</span>{/if}
              <span class="tiny muted ch">{e.channelName} · {fmtDuration(e.stop - e.start)}</span>
            </span>
            <span class="marks">
              {#if e.failed}<span class="mark bad" title={t('history.failed')}><Icon name="alert" size={15} /></span>{/if}
              {#if e.watched}<span class="mark good" title={t('history.watched')}><Icon name="eye" size={15} /></span>{/if}
              <span class="times" class:repeat={e.times > 1} title={timesHint(e)}>{e.times}×</span>
            </span>
          </div>
        {/each}
      </div>
    {:else}
      <div class="empty card">{search || channel || day ? t('history.noMatch') : t('history.empty')}</div>
    {/each}
    {#if more}
      <div class="row center">
        <button class="btn" onclick={() => load(true)} disabled={loading}>
          {#if loading}<span class="spinner"></span>{/if} {t('history.more')}</button>
      </div>
    {/if}
  {:else}
    <div class="center"><span class="spinner"></span></div>
  {/if}
  <p class="hint">{t('history.hint')}</p>
</div>

<style>
  .filters { display: flex; gap: 0.6rem; flex-wrap: wrap; margin-bottom: 1rem; }
  .search { position: relative; flex: 1 1 240px; display: flex; align-items: center; }
  .search :global(svg) { position: absolute; left: 0.7rem; color: var(--muted); pointer-events: none; }
  .search .input { padding-left: 2.1rem; width: 100%; }
  .filters select { flex: 0 1 220px; }
  .day { flex: 0 0 auto; }
  .day-head { margin: 1.1rem 0 0.4rem; font-size: 0.95rem; }
  .list { padding: 0.25rem 0; }
  .list .row { display: flex; align-items: center; gap: 0.9rem; padding: 0.5rem 1rem; border-top: 1px solid var(--border); }
  .list .row:first-child { border-top: 0; }
  .time { font-variant-numeric: tabular-nums; color: var(--text-2); min-width: 3.2rem; }
  .what { min-width: 0; }
  .ch { display: block; }
  .marks { display: inline-flex; align-items: center; gap: 0.45rem; }
  .mark { display: inline-flex; }
  .mark.good { color: var(--green); }
  .mark.bad { color: var(--red); }
  .times { font-size: 0.8rem; color: var(--muted); min-width: 2rem; text-align: right; font-variant-numeric: tabular-nums; }
  .times.repeat { color: var(--accent); font-weight: 600; }
  .center { display: flex; justify-content: center; padding: 1rem 0; }
</style>
