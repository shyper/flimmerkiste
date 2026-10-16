<script>
  import { t } from '../lib/i18n.svelte.js'
  import RuleGroup from './RuleGroup.svelte'
  import MediaPicker from './MediaPicker.svelte'
  import Toggle from './Toggle.svelte'
  import Icon from './Icon.svelte'
  import Segmented from './Segmented.svelte'
  import { untrack } from 'svelte'
  import { app } from '../lib/state.svelte.js'
  import { link } from '../lib/router.svelte.js'
  import { api } from '../lib/api.js'
  import { normalizeSource } from '../lib/config.js'

  // channel ({ kind, config }) is the channel being edited: trailers can follow its filter.
  let { source, channel = null } = $props()

  const ideas = $derived([1, 2, 3, 4, 5, 6, 7, 8].map((n) => t(`sourceEditor.idea${n}`)))

  // Sources created in the editor (new season, other filler type) start sparse.
  untrack(() => normalizeSource(source))

  // Trailers: how many titles currently have a trailer in a preferred language.
  let trailerCount = $state(null)
  let countTimer
  $effect(() => {
    if (source.type !== 'trailers') return
    const body = { source: $state.snapshot(source) }
    if (channel && source.channelFilter) body.channel = { kind: channel.kind, config: $state.snapshot(channel.config) }
    clearTimeout(countTimer)
    countTimer = setTimeout(async () => {
      try {
        trailerCount = (await api('POST', '/api/preview', body)).count
      } catch {
        trailerCount = null
      }
    }, 450)
  })

  let useFilter = $state(untrack(() => !!(source.ai?.filter?.rules?.length || source.ai?.filter?.groups?.length)))
  $effect(() => {
    if (source.type !== 'ai') return
    if (useFilter && !source.ai.filter) source.ai.filter = { match: 'all', rules: [{ field: 'type', op: 'is', values: ['movie'] }], groups: [] }
    if (!useFilter && source.ai.filter) source.ai.filter = undefined
  })
</script>

{#if source.type === 'rules'}
  <RuleGroup group={source.rules} />
{:else if source.type === 'trailers'}
  <div class="stack">
    <Segmented bind:value={source.trailerOrigin} size="sm" options={[
      { value: 'library', label: t('sourceEditor.originLibrary') },
      { value: 'discover', label: t('sourceEditor.originDiscover') },
      { value: 'all', label: t('sourceEditor.originAll') },
    ]} />
    <p class="hint">
      {#if source.trailerOrigin === 'library'} {t('sourceEditor.trailer_zu_filmen_die_du')} {:else} {t('sourceEditor.trailer_zu_neuen_und_kommenden')} <a href="/settings" use:link>{t('sourceEditor.einstellungen')}</a> {t('sourceEditor.auswaehlst')} {/if} {t('sourceEditor.es_laufen_nur_trailer_in')} </p>
    {#if channel && channel.kind !== 'manual'}
      <Toggle bind:checked={source.channelFilter} label={t('sourceEditor.filter_des_senders_uebernehmen')}
        hint={channel.kind === 'seasonal'
          ? t('sourceEditor.filterSeasonHint')
          : t('sourceEditor.filterChannelHint')} />
    {/if}
    <span class="label">{source.channelFilter ? t('sourceEditor.alsoOnly') : t('sourceEditor.only')}</span>
    <RuleGroup group={source.rules} />
    {#if trailerCount !== null}
      <div class="count" class:empty={!trailerCount}>
        <Icon name="film" size={14} />
        {#if trailerCount}{t('sourceEditor.titel_mit_trailer', [trailerCount])}{:else}{t('sourceEditor.noch_keine_passenden_trailer_sie')}{/if}
      </div>
    {/if}
  </div>
{:else if source.type === 'manual'}
  <MediaPicker bind:items={source.items} />
{:else if source.type === 'ai'}
  <div class="stack">
    {#if !app.meta?.aiEnabled}
      <div class="alert warn"><Icon name="alert" size={16} />
        <span>{t('sourceEditor.die_ki_integration_ist_noch')} <a href="/settings" use:link>{t('sourceEditor.einstellungen')}</a>{t('sourceEditor.bis_dahin_wird_zufaellig_aus')}</span>
      </div>
    {/if}
    <div class="field">
      <label class="label" for="theme">{t('sourceEditor.worum_soll_es_gehen')}</label>
      <textarea id="theme" class="input" rows="3" bind:value={source.ai.theme}
        placeholder={t('sourceEditor.beschreibe_das_thema_so_wie')}></textarea>
      <div class="ideas">
        {#each ideas as idea}
          <button type="button" class="chip" onclick={() => (source.ai.theme = idea)}><Icon name="sparkles" size={12} /> {idea}</button>
        {/each}
      </div>
    </div>
    <div class="grid2">
      <div class="field">
        <label class="label" for="batch">{t('sourceEditor.titel_pro_ki_anfrage')}</label>
        <input id="batch" class="input" type="number" min="3" max="40" bind:value={source.ai.batchSize} />
        <span class="hint">{t('sourceEditor.die_ki_plant_immer_die')}</span>
      </div>
      <div class="field">
        <label class="label" for="eps">{t('sourceEditor.episoden_am_stueck_bei_serien')}</label>
        <input id="eps" class="input" type="number" min="1" max="10" bind:value={source.ai.episodesPerShow} />
        <span class="hint">{t('sourceEditor.serien_laufen_in_episodenreihenfolge_wei')}</span>
      </div>
    </div>
    <Toggle bind:checked={useFilter} label={t('sourceEditor.auswahl_vorab_einschraenken')}
      hint={t('sourceEditor.z_b_nur_filme_nur')} />
    {#if useFilter && source.ai.filter}
      <div class="filter-box"><RuleGroup group={source.ai.filter} /></div>
    {/if}
  </div>
{/if}

<style>
  .ideas { display: flex; flex-wrap: wrap; gap: 0.35rem; margin-top: 0.5rem; }
  .ideas .chip { border: 0; cursor: pointer; font: inherit; font-size: 0.78rem; }
  .ideas .chip:hover { background: rgba(139, 92, 246, 0.2); color: #ddd6fe; }
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
  .count { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.82rem; color: var(--text-2); }
  .count.empty { color: var(--muted); }
  .filter-box { padding: 0.9rem; background: var(--bg-2); border-radius: var(--radius-sm); border: 1px solid var(--border); }
  @media (max-width: 640px) { .grid2 { grid-template-columns: 1fr; } }
</style>
