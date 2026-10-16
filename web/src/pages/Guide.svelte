<script>
  import { t } from '../lib/i18n.svelte.js'
  import { onMount, onDestroy } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import Modal from '../components/Modal.svelte'
  import { get } from '../lib/api.js'
  import { app } from '../lib/state.svelte.js'
  import { fmtTime, fmtDay, fmtDuration } from '../lib/format.js'

  const PX_PER_MIN = 5
  const HOURS = 6
  const SLOT = 30 * 60000

  const floorSlot = (t) => Math.floor(t / SLOT) * SLOT
  let from = $state(floorSlot(Date.now()) - SLOT)
  let rows = $state(null)
  let now = $state(Date.now())
  let detail = $state(null)
  let detailOpen = $state(false)
  let scroller = $state()
  let timer

  const to = $derived(from + HOURS * 3600000)
  const width = $derived(HOURS * 60 * PX_PER_MIN)
  const slots = $derived(Array.from({ length: HOURS * 2 }, (_, i) => from + i * SLOT))
  const x = (t) => ((t - from) / 60000) * PX_PER_MIN

  async function load() {
    rows = await get(`/api/guide?from=${from}&hours=${HOURS}`)
  }

  onMount(() => {
    load()
    timer = setInterval(() => { now = Date.now() }, 30000)
  })
  onDestroy(() => clearInterval(timer))

  function shift(hours) {
    from += hours * 3600000
    load()
  }
  function jumpNow() {
    from = floorSlot(Date.now()) - SLOT
    load()
    scroller?.scrollTo({ left: 0, behavior: 'smooth' })
  }
  // Prime time (20:00) today and tomorrow, as far as the guide is planned.
  const primeTimes = $derived.by(() => {
    const out = []
    for (const days of [0, 1, 2]) {
      const d = new Date(now)
      d.setDate(d.getDate() + days)
      d.setHours(20, 0, 0, 0)
      if (d.getTime() > now && d.getTime() < now + (app.meta?.horizon || 24) * 3600000) out.push(d.getTime())
    }
    return out
  })
  function jumpTo(ms) {
    from = floorSlot(ms) - SLOT
    load()
  }

  function open(p, ch) {
    detail = { ...p, channel: ch }
    detailOpen = true
  }
</script>

<div class="page wide">
  <div class="page-head">
    <div class="grow">
      <h1>{t('guide.programm')}</h1>
      <p>{t('guide.uhr', [fmtDay(from), fmtTime(from), fmtTime(to)])}</p>
    </div>
    <div class="row wrap">
      <button class="btn icon" onclick={() => shift(-3)} aria-label={t('guide.frueher')}><Icon name="left" /></button>
      <button class="btn" onclick={jumpNow}><Icon name="clock" size={16} /> {t('guide.jetzt')}</button>
      {#each primeTimes as p}
        <button class="btn" onclick={() => jumpTo(p)}>{t('guide.primeTime', [fmtDay(p), fmtTime(p)])}</button>
      {/each}
      <button class="btn icon" onclick={() => shift(3)} aria-label={t('guide.spaeter')}><Icon name="right" /></button>
    </div>
  </div>

  {#if rows === null}
    <div class="skeleton" style="height: 400px"></div>
  {:else if !rows.length}
    <div class="empty card">{t('guide.noch_keine_aktiven_sender')}</div>
  {:else}
    <div class="guide card" bind:this={scroller}>
      <div class="grid" style="width: {width + 200}px">
        <div class="corner"></div>
        <div class="times" style="width: {width}px">
          {#each slots as s}
            <span style="left: {x(s)}px">{fmtTime(s)}</span>
          {/each}
        </div>
        {#each rows as row (row.channel.id)}
          <div class="chan">
            <span class="cnum">{row.channel.number}</span>
            <img src={row.channel.logoUrl} alt={row.channel.name} title={row.channel.name} />
          </div>
          <div class="lane" style="width: {width}px">
            {#each row.programs as p (p.start)}
              {@const left = Math.max(0, x(p.start))}
              {@const right = Math.min(width, x(p.stop))}
              {#if right > left}
                <button class="prog" class:live={p.start <= now && p.stop > now} class:past={p.stop <= now}
                  style="left: {left}px; width: {right - left - 3}px" onclick={() => open(p, row.channel)}>
                  <span class="t ellipsis">{p.media.title}</span>
                  <span class="s ellipsis">{fmtTime(p.start)}{p.media.subtitle ? ' · ' + p.media.subtitle : p.media.year ? ' · ' + p.media.year : ''}</span>
                </button>
              {/if}
            {/each}
          </div>
        {/each}
        {#if now >= from && now <= to}
          <div class="nowline" style="left: {200 + x(now)}px"></div>
        {/if}
      </div>
    </div>
  {/if}
</div>

<Modal bind:open={detailOpen} title={detail?.channel.name || ''} wide>
  {#if detail}
    <div class="detail">
      {#if detail.media.thumb}<img class="poster" src={detail.media.thumb} alt="" />{/if}
      <div class="stack grow">
        <div>
          <h2>{detail.media.title}</h2>
          {#if detail.media.subtitle}<p class="muted">{detail.media.subtitle}</p>{/if}
        </div>
        <div class="row wrap">
          <span class="chip accent"><Icon name="clock" size={12} /> {fmtDay(detail.start)}, {fmtTime(detail.start)} – {fmtTime(detail.stop)}</span>
          <span class="chip">{fmtDuration(detail.stop - detail.start)}</span>
          {#if detail.media.year}<span class="chip">{detail.media.year}</span>{/if}
          {#if detail.media.contentRating}<span class="chip">{detail.media.contentRating}</span>{/if}
          {#if detail.media.rating}<span class="chip">★ {detail.media.rating.toFixed(1)}</span>{/if}
          {#if detail.media.watched}<span class="chip green"><Icon name="check" size={12} /> {t('guide.gesehen')}</span>{/if}
          {#if detail.source}<span class="chip violet">{detail.source}</span>{/if}
        </div>
        {#if detail.note}<div class="alert info small"><Icon name="sparkles" size={15} /> {detail.note}</div>{/if}
        {#if detail.media.genres?.length}<p class="small muted">{detail.media.genres.join(' · ')}</p>{/if}
        <p class="summary">{detail.media.summary || t('guide.noSummary')}</p>
      </div>
    </div>
  {/if}
</Modal>

<style>
  .wide { max-width: none; }
  .guide { padding: 0; overflow: auto; max-height: calc(100vh - 170px); }
  .grid { display: grid; grid-template-columns: 200px 1fr; position: relative; }
  .corner { position: sticky; left: 0; top: 0; z-index: 4; background: var(--surface); border-bottom: 1px solid var(--border); }
  .times { position: sticky; top: 0; z-index: 3; height: 36px; background: var(--surface); border-bottom: 1px solid var(--border); }
  .times span { position: absolute; top: 9px; font-size: 0.78rem; font-weight: 700; color: var(--muted); padding-left: 6px; border-left: 1px solid var(--border-strong); height: 26px; }
  .chan { position: sticky; left: 0; z-index: 2; height: 74px; display: flex; align-items: center; gap: 0.5rem; padding: 0 0.75rem; background: var(--surface); border-bottom: 1px solid var(--border); border-right: 1px solid var(--border); }
  .chan img { width: 140px; height: 60px; object-fit: contain; }
  .cnum { font-weight: 800; color: var(--muted); font-size: 0.8rem; width: 1.6rem; }
  .lane { position: relative; height: 74px; border-bottom: 1px solid var(--border);
    background-image: repeating-linear-gradient(90deg, transparent 0, transparent 149px, rgba(255,255,255,0.03) 149px, rgba(255,255,255,0.03) 150px); }
  .prog { position: absolute; top: 6px; bottom: 6px; display: flex; flex-direction: column; justify-content: center; gap: 2px; padding: 0 0.7rem; border-radius: 8px; border: 1px solid var(--border); background: var(--surface-2); color: var(--text); font: inherit; text-align: left; cursor: pointer; overflow: hidden; transition: background 0.15s, border-color 0.15s; }
  .prog:hover { background: var(--surface-3); border-color: var(--border-strong); z-index: 1; }
  .prog.live { background: linear-gradient(90deg, rgba(229, 160, 13, 0.22), rgba(229, 160, 13, 0.08)); border-color: rgba(229, 160, 13, 0.5); }
  .prog.past { opacity: 0.5; }
  .t { font-weight: 700; font-size: 0.88rem; }
  .s { font-size: 0.75rem; color: var(--muted); }
  .nowline { position: absolute; top: 0; bottom: 0; width: 2px; background: var(--red); z-index: 2; box-shadow: 0 0 10px rgba(239, 68, 68, 0.7); pointer-events: none; }
  .nowline::before { content: ''; position: absolute; top: 28px; left: -4px; width: 10px; height: 10px; border-radius: 50%; background: var(--red); }
  .detail { display: flex; gap: 1.25rem; }
  .poster { width: 180px; aspect-ratio: 2 / 3; object-fit: cover; border-radius: var(--radius-sm); background: var(--surface-3); flex-shrink: 0; }
  .summary { color: var(--text-2); }
  @media (max-width: 640px) {
    .detail { flex-direction: column; }
    .poster { width: 120px; }
    .grid { grid-template-columns: 110px 1fr; }
    .chan img { width: 70px; }
  }
</style>
