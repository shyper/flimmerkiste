<script>
  import Icon from './Icon.svelte'
  let { value = $bindable(), options = [], size = 'md', onchange } = $props()

  function choose(o) {
    value = o.value
    onchange?.(o.value)
  }
  // A radio group is one tab stop; the arrow keys move the choice (as with native radios).
  function onKey(e, i) {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key]
    let next = step ? (i + step + options.length) % options.length : e.key === 'Home' ? 0 : e.key === 'End' ? options.length - 1 : -1
    if (next < 0) return
    e.preventDefault()
    choose(options[next])
    e.currentTarget.parentElement.children[next]?.focus()
  }
  const current = $derived(Math.max(0, options.findIndex((o) => o.value === value)))
</script>

<div class="seg {size}" role="radiogroup">
  {#each options as o, i}
    <button type="button" role="radio" aria-checked={value === o.value} class:active={value === o.value}
      tabindex={i === current ? 0 : -1} onclick={() => choose(o)} onkeydown={(e) => onKey(e, i)} title={o.hint || undefined}>
      {#if o.icon}<Icon name={o.icon} size={15} />{/if}
      {o.label}
    </button>
  {/each}
</div>

<style>
  .seg { display: inline-flex; background: var(--bg-2); border: 1px solid var(--border-strong); border-radius: var(--radius-sm); padding: 3px; gap: 2px; flex-wrap: wrap; }
  button { display: inline-flex; align-items: center; gap: 0.35rem; border: 0; background: transparent; color: var(--text-2); font: inherit; font-size: 0.85rem; font-weight: 600; padding: 0.4rem 0.8rem; border-radius: 6px; cursor: pointer; transition: background 0.15s, color 0.15s; }
  button:hover { color: var(--text); }
  button.active { background: var(--surface-3); color: var(--text); box-shadow: 0 1px 2px rgba(0,0,0,0.3); }
  .sm button { padding: 0.25rem 0.6rem; font-size: 0.8rem; }
</style>
