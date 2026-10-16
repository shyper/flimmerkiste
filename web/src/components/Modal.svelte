<script>
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  let { open = $bindable(false), title = '', wide = false, children, footer } = $props()

  function onKey(e) {
    if (e.key === 'Escape') open = false
  }
</script>

<svelte:window onkeydown={open ? onKey : undefined} />

{#if open}
  <div class="backdrop" onclick={(e) => e.target === e.currentTarget && (open = false)} role="presentation">
    <div class="modal" class:wide role="dialog" aria-modal="true" aria-label={title}>
      <header>
        <h3>{title}</h3>
        <button class="btn ghost icon" onclick={() => (open = false)} aria-label={t('modal.schliessen')}><Icon name="x" /></button>
      </header>
      <div class="body">{@render children?.()}</div>
      {#if footer}<footer>{@render footer()}</footer>{/if}
    </div>
  </div>
{/if}

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(5, 6, 10, 0.7); backdrop-filter: blur(4px); z-index: 100; display: grid; place-items: center; padding: 1rem; animation: fade 0.15s; }
  .modal { width: min(560px, 100%); max-height: calc(100vh - 2rem); overflow: auto; background: var(--surface); border: 1px solid var(--border-strong); border-radius: 16px; box-shadow: var(--shadow); animation: pop 0.18s ease-out; }
  .modal.wide { width: min(900px, 100%); }
  header { display: flex; align-items: center; justify-content: space-between; padding: 1rem 1.25rem; border-bottom: 1px solid var(--border); position: sticky; top: 0; background: var(--surface); z-index: 1; }
  .body { padding: 1.25rem; }
  footer { display: flex; justify-content: flex-end; gap: 0.5rem; padding: 1rem 1.25rem; border-top: 1px solid var(--border); }
  @keyframes fade { from { opacity: 0; } }
  @keyframes pop { from { opacity: 0; transform: translateY(8px) scale(0.98); } }
</style>
