<script>
  // Breaks: aligned start times, filler clips, the up-next slate.
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../Icon.svelte'
  import Toggle from '../Toggle.svelte'
  import Segmented from '../Segmented.svelte'
  import SourceEditor from '../SourceEditor.svelte'

  let { ch = $bindable(), fillerOnly = false, effectiveTranscode = 'copy' } = $props()

  const alignOptions = [
    { value: 0, get label() { return t('channelEditor.align.0') } }, { value: 5, get label() { return t('channelEditor.align.5') } }, { value: 15, label: ':00 :15 :30 :45' },
    { value: 30, get label() { return t('channelEditor.align.30') } }, { value: 60, get label() { return t('channelEditor.align.60') } },
  ]
  let cardVersion = $state(0)
  const shortClips = () => ({ type: 'rules', rules: { match: 'all', rules: [{ field: 'duration', op: 'lt', values: ['10'] }], groups: [] } })
  function toggleFiller(on) {
    ch.config.filler = on ? { type: 'trailers', trailerOrigin: 'library', rules: { match: 'all', rules: [], groups: [] } } : undefined
  }
  function setFillerType(t) {
    if (t === 'manual') ch.config.filler = { type: 'manual', items: [] }
    else if (t === 'trailers') ch.config.filler = { type: 'trailers', trailerOrigin: 'library', rules: { match: 'all', rules: [], groups: [] } }
    else ch.config.filler = shortClips()
  }
</script>

<section class="card" id="sec-breaks">
  <div class="section-title"><Icon name="clock" /><h2>{t('channelEditor.sendezeiten_pausen')}</h2></div>
  <div class="stack">
    {#if fillerOnly}
      <p class="hint">{t('channelEditor.dieser_sender_hat_keine_eigenen')}</p>
    {:else}
    <div>
      <span class="label">{t('channelEditor.titel_beginnen')}</span>
      <Segmented bind:value={ch.config.align} size="sm" options={alignOptions} />
      <p class="hint">
        {#if ch.config.align} {t('channelEditor.jeder_titel_startet_zur_naechsten')} {:else} {t('channelEditor.titel_laufen_direkt_nacheinander_ohne')} {/if}
      </p>
    </div>
    <div>
      <span class="label">{t('channelEditor.clips_nach_jedem_titel')}</span>
      <Segmented bind:value={ch.config.fillerBetween} size="sm" options={[0, 1, 2, 3].map((n) => ({ value: n, label: n ? String(n) : t('channelEditor.fillerNone') }))} />
      <p class="hint">{t('channelEditor.wie_werbepausen_nur_mit_trailern')}</p>
    </div>
    {/if}
    {#if fillerOnly || ch.config.align || ch.config.fillerBetween}
      <div class="breaks">
        <Toggle checked={!!ch.config.filler} onchange={toggleFiller} label={t('channelEditor.trailer_oder_clips_zeigen')}
          hint={t('channelEditor.kinotrailer_ueber_plex_oder_eigene')} />
        {#if ch.config.filler}
          <Segmented value={ch.config.filler.type} onchange={setFillerType} size="sm" options={[
            { value: 'trailers', label: t('channelEditor.filler.trailers'), icon: 'film' },
            { value: 'rules', label: t('channelEditor.filler.rules'), icon: 'filter' }, { value: 'manual', label: t('channelEditor.filler.manual'), icon: 'list' },
          ]} />
          {#key ch.config.filler}
            <SourceEditor source={ch.config.filler} channel={ch} />
          {/key}
          {#if ch.config.filler.type === 'trailers' && effectiveTranscode === 'copy'}
            <p class="hint">{t('channelEditor.trailer_sind_h_264_stockt')}</p>
          {/if}
        {/if}
        {#if ch.config.align && !fillerOnly}
          <Toggle bind:checked={ch.config.previews} label={t('channelEditor.programmvorschau')}
            hint={t('channelEditor.am_ende_laengerer_pausen_laeuft')} />
        {/if}
        {#if ch.config.align}
          <div class="card-preview">
            <span class="label">{t('channelEditor.so_sieht_die_programmtafel_aus')}</span>
            {#if ch.id}
              <img src="/api/channels/{ch.id}/card?v={cardVersion}" alt={t('channelEditor.programmtafel')} />
              <button class="btn ghost sm" onclick={() => cardVersion++}><Icon name="refresh" size={14} /> {t('channelEditor.aktualisieren')}</button>
            {:else}
              <p class="hint">{t('channelEditor.die_vorschau_erscheint_nach_dem')}</p>
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </div>
</section>

<style>
  .breaks { display: flex; flex-direction: column; gap: 0.9rem; padding: 1rem; background: var(--bg-2); border: 1px solid var(--border); border-radius: var(--radius-sm); }
  .card-preview { display: flex; flex-direction: column; gap: 0.4rem; align-items: flex-start; }
  .card-preview img { width: 100%; max-width: 520px; aspect-ratio: 16 / 9; border-radius: var(--radius-sm); border: 1px solid var(--border); background: var(--surface-3); }
</style>
