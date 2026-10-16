<script>
  import { t } from '../lib/i18n.svelte.js'
  // Marks settings that only work with "Fully normalize".
</script>

<span class="chip full" title={t('settings.fullOnlyHint')}>{t('settings.fullOnly')}</span>

<style>
  .full { font-size: 0.7rem; vertical-align: middle; margin-left: 0.35rem; }
</style>
