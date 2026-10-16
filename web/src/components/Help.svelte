<script>
  import { t } from '../lib/i18n.svelte.js'
  // Details behind a "Learn more" toggle, so hints stay one sentence long.
  let { children } = $props()
  let open = $state(false)
</script>

<button type="button" class="more" aria-expanded={open} onclick={() => (open = !open)}>{open ? t('help.less') : t('help.more')}</button>
{#if open}<div class="help small muted">{@render children()}</div>{/if}

<style>
  .more { background: none; border: 0; padding: 0; color: var(--accent-2); font: inherit; font-size: 0.8rem; cursor: pointer; }
  .more:hover { text-decoration: underline; }
  .help { margin-top: 0.35rem; line-height: 1.5; }
</style>
