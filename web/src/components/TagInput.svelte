<script>
  import { t } from '../lib/i18n.svelte.js'
  // Multi-value input with autocompletion from library facets.
  let { values = $bindable([]), suggestions = [], placeholder = t('tagInput.placeholder'), numeric = false, label = '' } = $props()
  const listId = $props.id()
  let text = $state('')
  let open = $state(false)
  let active = $state(0)

  const filtered = $derived.by(() => {
    const q = text.trim().toLowerCase()
    const chosen = new Set(values.map((v) => v.toLowerCase()))
    return suggestions
      .filter((s) => !chosen.has(s.value.toLowerCase()) && (!q || s.value.toLowerCase().includes(q)))
      .slice(0, 12)
  })

  function add(v) {
    v = String(v).trim()
    if (!v || values.some((x) => x.toLowerCase() === v.toLowerCase())) return
    values = [...values, v]
    text = ''
    active = 0
  }

  function remove(i) {
    values = values.filter((_, j) => j !== i)
  }

  function onKey(e) {
    // Tab adds only what was typed or picked; otherwise it just leaves the field
    if (e.key === 'Tab' && !text.trim()) return
    if (e.key === 'Enter' || e.key === ',' || e.key === 'Tab') {
      if (open && filtered[active] && e.key !== ',') {
        e.preventDefault()
        add(filtered[active].value)
      } else if (text.trim()) {
        e.preventDefault()
        add(text)
      }
    } else if (e.key === 'Backspace' && !text && values.length) {
      remove(values.length - 1)
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      active = Math.min(active + 1, filtered.length - 1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      active = Math.max(active - 1, 0)
    } else if (e.key === 'Escape') {
      open = false
    }
  }
</script>

<div class="tags input" class:focus={open}>
  {#each values as v, i}
    <span class="tag">{v}<button type="button" onclick={() => remove(i)} aria-label={t('tagInput.entfernen')}>×</button></span>
  {/each}
  <input bind:value={text} {placeholder} inputmode={numeric ? 'numeric' : 'text'} aria-label={label || placeholder}
    role="combobox" aria-autocomplete="list" aria-expanded={open && filtered.length > 0} aria-controls={open && filtered.length ? listId : undefined}
    aria-activedescendant={open && filtered[active] ? `${listId}-${active}` : undefined}
    onfocus={() => (open = true)} onblur={() => setTimeout(() => { open = false; if (text.trim()) add(text) }, 150)}
    onkeydown={onKey} oninput={() => { open = true; active = 0 }} />
  {#if open && filtered.length}
    <ul class="menu" role="listbox" id={listId}>
      {#each filtered as s, i}
        <li role="option" id="{listId}-{i}" aria-selected={i === active} class:active={i === active}
          onmousedown={(e) => { e.preventDefault(); add(s.value) }}>
          <span class="ellipsis">{s.value}</span><span class="count">{s.count}</span>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .tags { display: flex; flex-wrap: wrap; gap: 0.3rem; align-items: center; position: relative; padding: 0.35rem 0.5rem; min-height: 40px; }
  .tags.focus { border-color: var(--accent); }
  .tag { display: inline-flex; align-items: center; gap: 0.2rem; background: rgba(229, 160, 13, 0.14); color: var(--accent-2); border-radius: 6px; padding: 0.1rem 0.25rem 0.1rem 0.5rem; font-size: 0.85rem; font-weight: 600; }
  .tag button { border: 0; background: none; color: inherit; cursor: pointer; font-size: 1rem; line-height: 1; padding: 0 0.2rem; opacity: 0.7; }
  .tag button:hover { opacity: 1; }
  input { flex: 1; min-width: 100px; border: 0; background: transparent; color: var(--text); font: inherit; font-size: 0.9rem; outline: none; padding: 0.2rem; }
  .menu { position: absolute; left: 0; right: 0; top: calc(100% + 4px); list-style: none; margin: 0; padding: 0.3rem; background: var(--surface-2); border: 1px solid var(--border-strong); border-radius: var(--radius-sm); box-shadow: var(--shadow); z-index: 30; max-height: 260px; overflow: auto; }
  .menu li { width: 100%; display: flex; justify-content: space-between; gap: 0.5rem; color: var(--text); font-size: 0.88rem; padding: 0.4rem 0.6rem; border-radius: 6px; cursor: pointer; text-align: left; }
  .menu li:hover, .menu li.active { background: var(--surface-3); }
  .count { color: var(--muted); font-size: 0.78rem; }
</style>
