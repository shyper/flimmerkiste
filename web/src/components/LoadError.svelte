<script>
  // Shown instead of a page part that could not load, with a way to try again.
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  let { message = '', retry } = $props()
</script>

<div class="alert warn load-error" role="alert">
  <Icon name="alert" size={16} />
  <span class="grow">{t('app.loadFailed', [message])}</span>
  {#if retry}<button class="btn sm" onclick={retry}>{t('app.retry')}</button>{/if}
</div>

<style>
  .load-error { display: flex; align-items: center; gap: 0.5rem; }
</style>
