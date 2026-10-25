<script>
  import { onMount } from 'svelte'
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../../components/Icon.svelte'
  import Modal from '../../components/Modal.svelte'
  import Segmented from '../../components/Segmented.svelte'
  import { get, post } from '../../lib/api.js'
  import { app, toast } from '../../lib/state.svelte.js'

  let { s } = $props()

  // ---- export ----
  let channels = $state([])
  let chosen = $state({})
  onMount(async () => {
    channels = await get('/api/channels').catch(() => [])
    chosen = Object.fromEntries(channels.map((c) => [c.id, true]))
  })
  const exportIds = $derived(channels.filter((c) => chosen[c.id]).map((c) => c.id))
  const exportUrl = $derived(`/api/export?channels=${exportIds.join(',')}`)

  // ---- import ----
  let file = $state(null) // parsed export file
  let preview = $state(null)
  let actions = $state({}) // index -> skip | new | replace
  let importing = $state(false)
  let importOpen = $state(false)
  let importInput

  async function pickFile(e) {
    const f = e.currentTarget.files?.[0]
    e.currentTarget.value = ''
    if (!f) return
    try {
      file = JSON.parse(await f.text())
    } catch {
      toast(t('settings.general.importBadFile'), 'error')
      return
    }
    try {
      preview = await post('/api/import/preview', file)
      actions = Object.fromEntries(preview.channels.map((c) => [c.index, c.error ? 'skip' : 'new']))
      importOpen = true
    } catch (err) {
      toast(err.message, 'error')
    }
  }

  async function runImport() {
    importing = true
    try {
      const choices = preview.channels.map((c) => ({ index: c.index, action: actions[c.index] }))
      const res = await post('/api/import', { file, choices })
      importOpen = false
      toast(t('settings.general.importDone', [res.created, res.replaced]), res.errors.length ? 'error' : 'success')
      for (const e of res.errors) toast(e, 'error')
      channels = await get('/api/channels').catch(() => channels)
    } catch (err) {
      toast(err.message, 'error')
    } finally {
      importing = false
    }
  }
</script>

<div class="stack">
  <div>
    <span class="label">{t('settings.general.language')}</span>
    <Segmented bind:value={s.language} size="sm" options={[{ value: 'de', label: 'Deutsch' }, { value: 'en', label: 'English' }]} />
    <span class="hint">{t('settings.general.languageHint')}</span>
  </div>

  <div class="block">
    <h3>{t('settings.general.export')}</h3>
    <p class="hint">{t('settings.general.exportHint')}</p>
    {#if channels.length}
      <div class="chans">
        {#each channels as c (c.id)}
          <label><input type="checkbox" bind:checked={chosen[c.id]} /> <span class="num">{c.number}</span> {c.name}</label>
        {/each}
      </div>
      <div class="row">
        <a class="btn sm" class:disabled={!exportIds.length} href={exportIds.length ? exportUrl : undefined} download>
          <Icon name="download" size={14} /> {t('settings.general.exportButton', [exportIds.length])}
        </a>
      </div>
    {:else}
      <p class="small muted">{t('settings.general.noChannels')}</p>
    {/if}
  </div>

  <div class="block">
    <h3>{t('settings.general.import')}</h3>
    <p class="hint">{t('settings.general.importHint')}</p>
    <!-- a real button, so the keyboard reaches it (a hidden input cannot take the focus) -->
    <button class="btn sm" onclick={() => importInput.click()}>
      <Icon name="upload" size={14} /> {t('settings.general.importButton')}
    </button>
    <input bind:this={importInput} type="file" accept="application/json,.json" onchange={pickFile} hidden />
  </div>

  <div>
    <span class="label">{t('settings.general.version')}</span>
    <span class="mono small">{app.meta?.version || '–'}</span>
  </div>
</div>

<Modal bind:open={importOpen} title={t('settings.general.importTitle')} wide>
  {#if preview}
    <p class="small muted">{t('settings.general.importIntro')}</p>
    <ul class="preview">
      {#each preview.channels as c (c.index)}
        <li>
          <div class="row">
            <span class="num">{c.number}</span>
            <b class="grow">{c.name}</b>
            {#if c.error}
              <span class="small bad">{c.error}</span>
            {:else}
              <select class="input auto" bind:value={actions[c.index]} aria-label={t('settings.general.importAction', [c.name])}>
                <option value="new">{c.conflict ? t('settings.general.actionNewNumber') : t('settings.general.actionImport')}</option>
                {#if c.conflict}<option value="replace">{t('settings.general.actionReplace', [c.conflict])}</option>{/if}
                <option value="skip">{t('settings.general.actionSkip')}</option>
              </select>
            {/if}
          </div>
          {#if c.missing.length}
            <details class="small">
              <summary class="warn">{t('settings.general.missing', [c.missing.length])}</summary>
              <span class="muted">{c.missing.join(' · ')}</span>
            </details>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (importOpen = false)}>{t('common.cancel')}</button>
    <button class="btn primary" onclick={runImport} disabled={importing || !Object.values(actions).some((a) => a !== 'skip')}>
      {#if importing}<span class="spinner"></span>{:else}<Icon name="upload" size={16} />{/if} {t('settings.general.importRun')}
    </button>
  {/snippet}
</Modal>

<style>
  .block { padding-top: 1rem; border-top: 1px solid var(--border); }
  .block h3 { margin: 0 0 0.3rem; font-size: 1rem; }
  .chans { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 0.3rem 0.75rem; margin: 0.5rem 0; padding: 0.5rem; background: var(--bg-2); border-radius: var(--radius-sm); max-height: 240px; overflow: auto; }
  .chans label { display: flex; align-items: center; gap: 0.45rem; font-size: 0.88rem; cursor: pointer; }
  .num { display: inline-block; min-width: 1.6rem; font-weight: 800; color: var(--muted); font-size: 0.8rem; }
  .disabled { opacity: 0.5; pointer-events: none; }
  .preview { list-style: none; padding: 0; margin: 0.75rem 0 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .preview li { padding: 0.6rem 0.75rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  .auto { width: auto; }
  .bad { color: var(--red); }
  .warn { color: var(--accent-2); cursor: pointer; margin-top: 0.3rem; }
</style>
