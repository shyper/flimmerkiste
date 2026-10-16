<script>
  import { app } from '../lib/state.svelte.js'
  import Icon from './Icon.svelte'
</script>

<div class="toasts" aria-live="polite">
  {#each app.toasts as t (t.id)}
    <div class="toast {t.kind}">
      <Icon name={t.kind === 'error' ? 'alert' : t.kind === 'success' ? 'check' : 'info'} size={16} />
      <span>{t.message}</span>
    </div>
  {/each}
</div>

<style>
  .toasts { position: fixed; bottom: 1.25rem; right: 1.25rem; display: flex; flex-direction: column; gap: 0.5rem; z-index: 200; max-width: calc(100vw - 2.5rem); }
  .toast { display: flex; align-items: center; gap: 0.6rem; padding: 0.7rem 1rem; border-radius: 10px; background: var(--surface-2); border: 1px solid var(--border-strong); box-shadow: var(--shadow); font-size: 0.9rem; animation: slide 0.2s ease-out; }
  .toast.success { border-color: rgba(34, 197, 94, 0.4); }
  .toast.success :global(.icon) { color: var(--green); }
  .toast.error { border-color: rgba(239, 68, 68, 0.45); }
  .toast.error :global(.icon) { color: var(--red); }
  @keyframes slide { from { opacity: 0; transform: translateY(10px); } }
</style>
