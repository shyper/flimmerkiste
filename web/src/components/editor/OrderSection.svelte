<script>
  // Order of the titles: shuffled or fixed, and how repeats are kept apart.
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../Icon.svelte'
  import Toggle from '../Toggle.svelte'
  import Segmented from '../Segmented.svelte'

  let { ch = $bindable() } = $props()

  const repeatOptions = [
    { value: 0, get label() { return t('channelEditor.repeat.0') } }, { value: 12, get label() { return t('channelEditor.repeat.12') } }, { value: 48, get label() { return t('channelEditor.repeat.48') } },
    { value: 168, get label() { return t('channelEditor.repeat.168') } }, { value: 720, get label() { return t('channelEditor.repeat.720') } },
  ]
</script>

<section class="card" id="sec-order">
  <div class="section-title"><Icon name="shuffle" /><h2>{t('channelEditor.ablauf')}</h2></div>
  <div class="stack">
    <div>
      <span class="label">{t('channelEditor.reihenfolge')}</span>
      <Segmented bind:value={ch.config.order} options={[
        { value: 'shuffle', label: t('channelEditor.order.shuffle'), icon: 'shuffle' },
        { value: 'fixed', label: t('channelEditor.order.fixed'), icon: 'order' },
      ]} />
      <p class="hint">
        {#if ch.config.order === 'fixed'} {t('channelEditor.titel_laufen_immer_in_derselben')} {:else} {t('channelEditor.alle_titel_kommen_einmal_dran')} {/if}
      </p>
    </div>
    {#if ch.config.order === 'fixed' && ch.kind !== 'manual'}
      <div class="field narrow">
        <label class="label" for="sort">{t('channelEditor.sortieren_nach')}</label>
        <select id="sort" class="input" bind:value={ch.config.sortBy}>
          <option value="">{t('channelEditor.titel')}</option>
          <option value="year">{t('channelEditor.erscheinungsjahr')}</option>
          <option value="release">{t('channelEditor.erscheinungsdatum')}</option>
          <option value="added">{t('channelEditor.zuletzt_hinzugefuegt')}</option>
        </select>
      </div>
    {/if}
    {#if ch.config.order === 'shuffle'}
      <Toggle bind:checked={ch.config.keepEpisodeOrder} label={t('channelEditor.serien_in_episodenreihenfolge')}
        hint={t('channelEditor.zwischen_filmen_und_serien_wird')} />
      <div>
        <span class="label">{t('channelEditor.wiederholungen_vermeiden_fuer')}</span>
        <Segmented bind:value={ch.config.noRepeatHours} size="sm" options={repeatOptions} />
      </div>
    {/if}
  </div>
</section>

<style>
  .narrow { max-width: 260px; }
</style>
