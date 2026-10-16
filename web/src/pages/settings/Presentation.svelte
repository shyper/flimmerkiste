<script>
  import { t } from '../../lib/i18n.svelte.js'
  import Help from '../../components/Help.svelte'
  import Toggle from '../../components/Toggle.svelte'
  import Segmented from '../../components/Segmented.svelte'
  import FullOnly from '../../components/FullOnly.svelte'

  let { s } = $props()
</script>

<div class="stack">
  <div>
    <Toggle bind:checked={s.cardMusic} label={t('settings.presentation.music')} hint={t('settings.presentation.musicHint')} />
    <Help>{t('settings.presentation.musicMore')}</Help>
  </div>
  <div>
    <span class="label">{t('settings.presentation.credits')}<FullOnly /></span>
    <Segmented bind:value={s.credits} size="sm" options={[
      { value: 'off', label: t('credits.off') }, { value: 'squeeze', label: t('credits.squeeze') },
    ]} />
    <span class="hint">{t('settings.presentation.creditsHint')}</span>
    <Help>{t('settings.presentation.creditsMore')}</Help>
  </div>
  <div>
    <Toggle bind:checked={s.trailerCaption} label={t('settings.presentation.caption')} hint={t('settings.presentation.captionHint')} />
    <FullOnly />
  </div>
</div>
