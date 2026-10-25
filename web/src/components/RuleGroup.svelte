<script>
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  import TagInput from './TagInput.svelte'
  import Self from './RuleGroup.svelte'
  import { untrack } from 'svelte'
  import { app } from '../lib/state.svelte.js'

  let { group, depth = 0, onremove } = $props()

  const opsFor = { year: ['gt', 'lt'], addedDays: ['gt', 'lt'] }
  const matchModes = ['all', 'any', 'none'].map((value) => ({
    value,
    get label() { return t(`ruleGroup.match.${value}`) },
    get hint() { return t(`ruleGroup.match.${value}.hint`) },
  }))
  const colors = ['var(--accent)', 'var(--violet)', 'var(--cyan)', 'var(--pink)']

  const fields = $derived(app.meta?.fields || [])
  const fieldById = $derived(Object.fromEntries(fields.map((f) => [f.id, f])))

  // One-time normalisation of older/partial definitions.
  untrack(() => {
    group.rules ??= []
    group.groups ??= []
    group.match ||= 'all'
  })

  function addRule() {
    group.rules.push({ field: 'genre', op: 'is', values: [] })
  }
  function addGroup() {
    group.groups.push({ match: 'any', rules: [{ field: 'genre', op: 'is', values: [] }], groups: [] })
  }
  function changeField(rule, id) {
    rule.field = id
    const f = fieldById[id]
    if (f && !f.ops.includes(rule.op)) rule.op = f.ops[0]
    rule.values = id === 'watched' ? ['false'] : id === 'type' ? ['movie'] : []
  }
  function opLabel(field, op) {
    if (opsFor[field]?.includes(op)) return t(`ruleGroup.op.${field}.${op}`)
    const key = `ruleGroup.op.${op}`
    const label = t(key)
    return label === key ? op : label
  }
  function facet(field) {
    return app.facets?.[field] || []
  }
</script>

<div class="group" class:nested={depth > 0} style="--c: {colors[depth % colors.length]}">
  <div class="head">
    <span class="muted small">{t('ruleGroup.passt_wenn')}</span>
    <select class="input mini" bind:value={group.match}>
      {#each matchModes as m}<option value={m.value}>{m.label}</option>{/each}
    </select>
    <span class="muted small">{group.match === 'none' ? t('ruleGroup.der_folgenden_bedingungen_none') : t('ruleGroup.der_folgenden_bedingungen')}</span>
    <span class="spacer"></span>
    {#if onremove}
      <button class="btn ghost icon sm" onclick={onremove} title={t('ruleGroup.gruppe_entfernen')}><Icon name="trash" size={15} /></button>
    {/if}
  </div>

  {#each group.rules as rule, i (rule)}
    {@const f = fieldById[rule.field]}
    <div class="rule">
      <select class="input field-sel" value={rule.field} onchange={(e) => changeField(rule, e.currentTarget.value)}>
        {#each fields as fo}<option value={fo.id}>{fo.label}</option>{/each}
      </select>
      {#if f && f.ops.length > 1}
        <select class="input op-sel" bind:value={rule.op}>
          {#each f.ops as op}<option value={op}>{opLabel(rule.field, op)}</option>{/each}
        </select>
      {/if}
      <div class="value">
        {#if rule.op === 'exists' || rule.op === 'missing'}
          <span class="muted small">–</span>
        {:else if rule.field === 'type'}
          <select class="input" value={rule.values[0] || 'movie'} onchange={(e) => (rule.values = [e.currentTarget.value])}>
            <option value="movie">{t('ruleGroup.film')}</option>
            <option value="episode">{t('ruleGroup.serien_episode')}</option>
          </select>
        {:else if rule.field === 'library'}
          <select class="input" value={rule.values[0] || ''} onchange={(e) => (rule.values = [e.currentTarget.value])}>
            <option value="" disabled>{t('ruleGroup.bibliothek_waehlen')}</option>
            {#each app.meta?.libraries || [] as l}<option value={l.id}>{l.title}</option>{/each}
          </select>
        {:else if rule.field === 'watched'}
          <select class="input" value={rule.values[0] || 'false'} onchange={(e) => (rule.values = [e.currentTarget.value])}>
            <option value="false">{t('ruleGroup.nein_noch_nicht_gesehen')}</option>
            <option value="true">{t('ruleGroup.ja_schon_gesehen')}</option>
          </select>
        {:else if rule.field === 'decade'}
          <div class="decades">
            {#each facet('decade') as d}
              {@const on = rule.values.includes(d.value)}
              <button type="button" class="chip" class:accent={on}
                onclick={() => (rule.values = on ? rule.values.filter((v) => v !== d.value) : [...rule.values, d.value])}> {t('ruleGroup.er', [d.value.slice(2)])} </button>
            {:else}
              <TagInput bind:values={rule.values} placeholder="z. B. 1980" numeric />
            {/each}
          </div>
        {:else if f?.kind === 'number'}
          <div class="row">
            <input class="input num" type="number" step="any" value={rule.values[0] ?? ''}
              oninput={(e) => (rule.values[0] = e.currentTarget.value)} placeholder={t('ruleGroup.wert')} />
            {#if rule.op === 'between'}
              <span class="muted small">{t('ruleGroup.und')}</span>
              <input class="input num" type="number" step="any" value={rule.values[1] ?? ''}
                oninput={(e) => (rule.values[1] = e.currentTarget.value)} placeholder={t('ruleGroup.wert')} />
            {/if}
            {#if f.unit}<span class="muted small">{f.unit}</span>{/if}
          </div>
        {:else}
          <TagInput bind:values={rule.values} suggestions={facet(rule.field)}
            placeholder={f?.kind === 'text' ? t('ruleGroup.enterText') : t('ruleGroup.pickOrType')} />
        {/if}
      </div>
      <button class="btn ghost icon sm" onclick={() => group.rules.splice(i, 1)} title={t('ruleGroup.bedingung_entfernen')}>
        <Icon name="x" size={15} />
      </button>
    </div>
    {#if i < group.rules.length - 1 || group.groups.length}
      <div class="joiner">{group.match === 'all' ? t('ruleGroup.and') : t('ruleGroup.or')}</div>
    {/if}
  {/each}

  {#each group.groups as sub, i (sub)}
    <Self group={sub} depth={depth + 1} onremove={() => group.groups.splice(i, 1)} />
    {#if i < group.groups.length - 1}
      <div class="joiner">{group.match === 'all' ? t('ruleGroup.and') : t('ruleGroup.or')}</div>
    {/if}
  {/each}

  <div class="actions">
    <button class="btn sm" onclick={addRule}><Icon name="plus" size={14} /> {t('ruleGroup.bedingung')}</button>
    {#if depth < 3}
      <button class="btn sm ghost" onclick={addGroup}><Icon name="layers" size={14} /> {t('ruleGroup.gruppe')}</button>
    {/if}
  </div>
</div>

<style>
  .group { display: flex; flex-direction: column; gap: 0.5rem; }
  .group.nested { border-left: 3px solid var(--c); background: rgba(255, 255, 255, 0.02); padding: 0.75rem 0.75rem 0.75rem 1rem; border-radius: 0 var(--radius-sm) var(--radius-sm) 0; }
  .head { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  .mini { width: auto; padding: 0.3rem 2rem 0.3rem 0.6rem; font-weight: 700; color: var(--c); }
  .rule { display: grid; grid-template-columns: 170px auto 1fr auto; gap: 0.5rem; align-items: start; }
  .field-sel { font-weight: 600; }
  .op-sel { width: auto; min-width: 120px; }
  .value { min-width: 0; }
  .num { width: 120px; }
  .decades { display: flex; flex-wrap: wrap; gap: 0.35rem; padding-top: 0.35rem; }
  .decades .chip { cursor: pointer; border: 0; font: inherit; font-size: 0.8rem; font-weight: 700; padding: 0.3rem 0.7rem; }
  .joiner { font-size: 0.72rem; font-weight: 800; text-transform: uppercase; letter-spacing: 0.08em; color: var(--c); padding-left: 0.4rem; }
  .actions { display: flex; gap: 0.4rem; padding-top: 0.25rem; }
  @media (max-width: 760px) {
    .rule { grid-template-columns: 1fr auto; }
    .rule .value, .rule .op-sel { grid-column: 1 / 2; }
  }
</style>
