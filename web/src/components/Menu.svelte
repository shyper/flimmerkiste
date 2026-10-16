<script>
  import Icon from './Icon.svelte'
  // A "more" button with a small list of actions: items = [{ label, icon, onclick }].
  let { items, label } = $props()
  let open = $state(false)
  let root = $state()

  function onDocClick(e) {
    if (open && root && !root.contains(e.target)) open = false
  }
  function onKey(e) {
    if (e.key === 'Escape') open = false
  }
</script>

<svelte:document onclick={onDocClick} onkeydown={onKey} />

<div class="menu" bind:this={root}>
  <button class="btn icon" onclick={() => (open = !open)} aria-label={label} title={label} aria-haspopup="menu" aria-expanded={open}><Icon name="more" /></button>
  {#if open}
    <div class="list" role="menu">
      {#each items as it}
        <button role="menuitem" onclick={() => ((open = false), it.onclick())}>{#if it.icon}<Icon name={it.icon} size={15} />{/if} {it.label}</button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .menu { position: relative; }
  .list { position: absolute; right: 0; top: calc(100% + 4px); min-width: 220px; z-index: 30; padding: 0.3rem; background: var(--surface-2); border: 1px solid var(--border-strong); border-radius: var(--radius-sm); box-shadow: var(--shadow); }
  .list button { display: flex; align-items: center; gap: 0.5rem; width: 100%; padding: 0.5rem 0.6rem; border: 0; border-radius: 6px; background: none; color: var(--text); font: inherit; font-size: 0.9rem; text-align: left; cursor: pointer; }
  .list button:hover, .list button:focus-visible { background: var(--surface-3); }
</style>
