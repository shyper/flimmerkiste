<script>
  let { checked = $bindable(false), label = '', hint = '', disabled = false, onchange } = $props()
</script>

<label class="toggle" class:disabled>
  <input type="checkbox" bind:checked {disabled} onchange={() => onchange?.(checked)} />
  <span class="track"><span class="thumb"></span></span>
  {#if label}
    <span class="text">
      <span class="t">{label}</span>
      {#if hint}<span class="h">{hint}</span>{/if}
    </span>
  {/if}
</label>

<style>
  .toggle { display: inline-flex; align-items: flex-start; gap: 0.7rem; cursor: pointer; user-select: none; }
  .toggle.disabled { opacity: 0.5; cursor: not-allowed; }
  input { position: absolute; opacity: 0; pointer-events: none; }
  .track { width: 40px; height: 22px; border-radius: 999px; background: var(--surface-3); border: 1px solid var(--border-strong); position: relative; transition: background 0.2s; flex-shrink: 0; margin-top: 1px; }
  .thumb { position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: 50%; background: var(--text-2); transition: transform 0.2s, background 0.2s; }
  input:checked + .track { background: var(--accent); border-color: var(--accent); }
  input:checked + .track .thumb { transform: translateX(18px); background: white; }
  input:focus-visible + .track { box-shadow: 0 0 0 3px rgba(229, 160, 13, 0.3); }
  .text { display: flex; flex-direction: column; }
  .t { font-weight: 600; font-size: 0.9rem; }
  .h { font-size: 0.8rem; color: var(--muted); }
</style>
