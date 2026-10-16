<script>
  import { t } from '../../lib/i18n.svelte.js'
  import Help from '../../components/Help.svelte'
  import Segmented from '../../components/Segmented.svelte'
  import FullOnly from '../../components/FullOnly.svelte'

  let { s } = $props()
</script>

<div class="stack">
  <div>
    <span class="label">{t('settings.hardware.encode')}<FullOnly /></span>
    <Segmented bind:value={s.hwAccel} size="sm" options={[{ value: 'auto', label: t('settings.auto') }, { value: 'off', label: t('settings.off') }]} />
    <span class="hint">{s.hwAvailable ? t('settings.hardware.active', [s.hwStatus]) : t('settings.hardware.unavailable', [s.hwStatus])}</span>
    <Help>{t('settings.hardware.encodeMore')}</Help>
  </div>
  {#if s.hwAvailable && s.hwAccel !== 'off'}
    <div>
      <span class="label">{t('settings.hardware.decode')}</span>
      <Segmented bind:value={s.hwDecode} size="sm" options={[{ value: 'auto', label: t('settings.auto') }, { value: 'off', label: t('settings.off') }]} />
      <span class="hint">{t('settings.hardware.decodeResult', [s.hwDecodeStatus])}</span>
      <Help>{t('settings.hardware.decodeMore')}</Help>
    </div>
  {/if}
</div>
