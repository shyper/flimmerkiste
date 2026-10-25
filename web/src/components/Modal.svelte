<script>
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  let { open = $bindable(false), title = '', wide = false, children, footer } = $props()

  function onKey(e) {
    if (e.key === 'Escape') open = false
  }

  const focusable = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
  // trap keeps the keyboard inside the open dialog and gives the focus back
  // to where it was when the dialog closes.
  function trap(node) {
    const before = document.activeElement
    const first = node.querySelector('.body ' + focusable) || node.querySelector('footer ' + focusable) || node.querySelector(focusable)
    ;(first || node).focus()
    const onTab = (e) => {
      if (e.key !== 'Tab') return
      const all = [...node.querySelectorAll(focusable)]
      if (!all.length) return e.preventDefault()
      const i = all.indexOf(document.activeElement)
      if (e.shiftKey && i <= 0) {
        e.preventDefault()
        all.at(-1).focus()
      } else if (!e.shiftKey && i === all.length - 1) {
        e.preventDefault()
        all[0].focus()
      }
    }
    node.addEventListener('keydown', onTab)
    return {
      destroy() {
        node.removeEventListener('keydown', onTab)
        if (before instanceof HTMLElement && before.isConnected) before.focus()
      },
    }
  }
</script>

<svelte:window onkeydown={open ? onKey : undefined} />

{#if open}
  <div class="backdrop" onclick={(e) => e.target === e.currentTarget && (open = false)} role="presentation">
    <div class="modal" class:wide role="dialog" aria-modal="true" aria-label={title} tabindex="-1" use:trap>
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
  .modal:focus { outline: none; }
  header { display: flex; align-items: center; justify-content: space-between; padding: 1rem 1.25rem; border-bottom: 1px solid var(--border); position: sticky; top: 0; background: var(--surface); z-index: 1; }
  .body { padding: 1.25rem; }
  footer { display: flex; justify-content: flex-end; gap: 0.5rem; padding: 1rem 1.25rem; border-top: 1px solid var(--border); }
  @keyframes fade { from { opacity: 0; } }
  @keyframes pop { from { opacity: 0; transform: translateY(8px) scale(0.98); } }
</style>
