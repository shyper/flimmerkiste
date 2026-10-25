<script>
  // The side card of the editor: the logo as it airs, what the content finds,
  // and what comes up next.
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../Icon.svelte'
  import { fmtDuration, fmtTime, fmtDay } from '../../lib/format.js'

  let { ch, currentLogo = '', preview = null, previewLoading = false, previewSource = null, currentSeason = null,
    planning = false, usesAI = false, upcoming = [] } = $props()
</script>

<div class="card sticky">
  <div class="logo-preview" style="--c: {ch.color || 'var(--accent)'}">
    <img src={currentLogo} alt="" />
  </div>
  <h3 class="mt">{t('channelEditor.vorschau')} {#if ch.kind === 'seasonal'}
      <span class="muted small">{previewSource ? t('channelEditor.previewSelection') : currentSeason ? t('channelEditor.seasonRunning', [currentSeason.emoji || '', currentSeason.name]) : t('channelEditor.restOfYear')}</span>
    {/if}
  </h3>
  {#if previewLoading && !preview}
    <div class="row mt"><span class="spinner"></span></div>
  {:else if preview}
    <div class="stats" class:loading={previewLoading}>
      <div><b>{preview.count}</b><span>{t('channelEditor.titleCount')}</span></div>
      <div><b>{fmtDuration(preview.durationMs)}</b><span>{t('channelEditor.programm')}</span></div>
      {#if preview.shows}<div><b>{preview.shows}</b><span>{t('channelEditor.serien')}</span></div>{/if}
    </div>
    {#if ch.kind === 'ai'}
      <p class="tiny muted">{t('channelEditor.aus_diesen_titeln_waehlt_die')}</p>
    {/if}
    {#if preview.count === 0}
      <div class="alert warn small"><Icon name="alert" size={15} /> {t('channelEditor.keine_treffer_passe_die_bedingungen')}</div>
    {/if}
    <div class="posters">
      {#each preview.items.slice(0, 18) as m (m.key)}
        <img src={m.thumb} alt={m.title} title="{m.title}{m.year ? ` (${m.year})` : ''}" loading="lazy" />
      {/each}
    </div>
  {/if}

  {#if ch.id && (upcoming.length || planning)}
    <h3 class="mt">{t('channelEditor.als_naechstes')}</h3>
    {#if planning}
      <div class="row tiny muted planning" role="status"><span class="spinner"></span> {usesAI ? t('channelEditor.aiPlanning') : t('channelEditor.planning')}</div>
    {/if}
    <ul class="upcoming">
      {#each upcoming as p}
        <li>
          <span class="time">{fmtTime(p.start)}<small>{fmtDay(p.start)}</small></span>
          <span class="grow ellipsis">
            {p.media.title}
            {#if p.media.subtitle}<span class="muted tiny block ellipsis">{p.media.subtitle}</span>{/if}
            {#if p.note}<span class="note tiny block ellipsis" title={p.note}>💡 {p.note}</span>{/if}
          </span>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .mt { margin-top: 1rem; }
  .sticky { position: sticky; top: 1rem; }
  .logo-preview { aspect-ratio: 16 / 9; border-radius: var(--radius-sm); display: grid; place-items: center; overflow: hidden;
    background: radial-gradient(circle at 50% 35%, color-mix(in srgb, var(--c) 22%, #23252f), #121318 75%); }
  .logo-preview img { width: 92%; height: 92%; object-fit: contain; }
  .stats { display: flex; gap: 0.5rem; margin: 0.6rem 0; transition: opacity 0.2s; }
  .stats.loading { opacity: 0.5; }
  .stats div { flex: 1; display: flex; flex-direction: column; background: var(--bg-2); border-radius: var(--radius-sm); padding: 0.5rem 0.6rem; }
  .stats b { font-size: 1.05rem; }
  .stats span { font-size: 0.72rem; color: var(--muted); }
  .posters { display: grid; grid-template-columns: repeat(6, 1fr); gap: 4px; margin-top: 0.6rem; }
  .posters img { width: 100%; aspect-ratio: 2 / 3; object-fit: cover; border-radius: 4px; background: var(--surface-3); }
  .upcoming { list-style: none; padding: 0; margin: 0.5rem 0 0; display: flex; flex-direction: column; gap: 0.5rem; max-height: 360px; overflow: auto; }
  .upcoming li { display: flex; gap: 0.7rem; font-size: 0.88rem; }
  .time { font-weight: 700; color: var(--accent-2); width: 3.4rem; flex-shrink: 0; display: flex; flex-direction: column; font-size: 0.85rem; }
  .time small { color: var(--muted); font-weight: 500; font-size: 0.7rem; }
  .block { display: block; }
  .note { color: #c4b5fd; }
  .planning { margin-top: 0.5rem; }
  .planning .spinner { width: 14px; height: 14px; }
  @media (max-width: 1100px) {
    .sticky { position: static; }
    .posters { grid-template-columns: repeat(9, 1fr); }
  }
</style>
