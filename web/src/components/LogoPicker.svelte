<script>
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  import Toggle from './Toggle.svelte'
  import { untrack } from 'svelte'
  import { app, toast } from '../lib/state.svelte.js'
  import { api } from '../lib/api.js'
  import { logoPreviewUrl, holidays, hueThemes } from '../lib/logo.js'

  // config is the channel config; logo options are stored on it directly.
  let { name = '', config, seasonal = false, number = 0, color = $bindable(''), hint = '', customLogo = '', posterSource = null, onupload, ondelete } = $props()

  let holidayPreview = $state('')
  let posterBusy = $state(false)

  async function colorFromPosters() {
    if (!posterSource) return
    posterBusy = true
    try {
      const res = await api('POST', '/api/poster-color', { source: posterSource })
      color = res.color
      toast(t('logoPicker.colorFromPosters'), 'success')
    } catch (e) {
      toast(e.message, 'error')
    } finally {
      posterBusy = false
    }
  }

  const swatches = ['', '#e5a00d', '#ef4444', '#ec4899', '#8b5cf6', '#3b82f6', '#06b6d4', '#10b981', '#f97316']

  // Debounce the name so typing doesn't render dozens of previews.
  let debounced = $state(untrack(() => name))
  $effect(() => {
    const n = name
    const t = setTimeout(() => (debounced = n), 400)
    return () => clearTimeout(t)
  })

  // Tagline is debounced like the name.
  let tagDebounced = $state(untrack(() => config.logoTagline || ''))
  $effect(() => {
    const v = config.logoTagline || ''
    const t = setTimeout(() => (tagDebounced = v), 400)
    return () => clearTimeout(t)
  })
  const url = (s) => logoPreviewUrl({
    name: debounced, style: s, color, hint, seed: config.logoSeed, hue: config.logoHue, tagline: tagDebounced,
    number: config.logoNumber ? number : 0, holiday: config.logoHoliday ? holidayPreview : '',
  })
  const styles = $derived(app.meta?.logoStyles || [])

  let fileInput
  let dragOver = $state(false)

  function onDrop(e) {
    e.preventDefault()
    dragOver = false
    const f = e.dataTransfer?.files?.[0]
    if (f) onupload?.(f)
  }
</script>

<div class="logo-picker">
  {#if customLogo}
    <div class="custom">
      <div class="logo-frame big"><img src={customLogo} alt={t('logoPicker.senderlogo')} /></div>
      <div class="stack">
        <p class="small">{t('logoPicker.eigenes_logo_wird_verwendet')}</p>
        <div class="row">
          <button class="btn sm" onclick={() => fileInput.click()}><Icon name="upload" size={14} /> {t('logoPicker.ersetzen')}</button>
          <button class="btn sm danger" onclick={ondelete}><Icon name="trash" size={14} /> {t('logoPicker.entfernen_generator_nutzen')}</button>
        </div>
      </div>
    </div>
  {:else}
    <div class="gallery">
      <button type="button" class="style upload-tile" class:over={dragOver} onclick={() => fileInput.click()}
        ondragover={(e) => { e.preventDefault(); dragOver = true }} ondragleave={() => (dragOver = false)} ondrop={onDrop}
        title={t('logoPicker.png_jpg_webp_oder_gif')}>
        <div class="logo-frame drop"><Icon name="upload" size={26} /><span class="tiny">{t('logoPicker.bild_waehlen_oder_hierher_ziehen')}</span></div>
        <span class="label-row">{t('logoPicker.eigenes_logo')}</span>
      </button>
      {#each styles as s (s.id)}
        <button type="button" class="style" class:active={config.logoStyle === s.id || (!config.logoStyle && s.id === 'auto')} onclick={() => (config.logoStyle = s.id)} title={s.hint}>
          <div class="logo-frame"><img src={url(s.id)} alt={s.label} loading="lazy" /></div>
          <span class="label-row">{#if s.id === 'auto'}<Icon name="wand" size={13} />{/if}{s.label}</span>
        </button>
      {/each}
    </div>
    <div class="row wrap colors">
      <span class="label nomb">{t('logoPicker.akzentfarbe')}</span>
      {#each swatches as c}
        <button type="button" class="swatch" class:active={color === c} style="--sw: {c || 'conic-gradient(#ef4444, #facc15, #22c55e, #3b82f6, #a855f7, #ef4444)'}"
          title={c || t('logoPicker.automatic')} onclick={() => (color = c)} aria-label={c || t('logoPicker.automatic')}></button>
      {/each}
      <label class="swatch custom-color" title={t('logoPicker.eigene_farbe')}>
        <input type="color" value={color || '#e5a00d'} oninput={(e) => (color = e.currentTarget.value)} />
      </label>
      {#if posterSource}
        <button type="button" class="btn sm ghost" onclick={colorFromPosters} disabled={posterBusy} title={t('logoPicker.farbe_passend_zu_den_postern')}>
          {#if posterBusy}<span class="spinner"></span>{:else}<Icon name="eye" size={14} />{/if} {t('logoPicker.aus_postern')} </button>
      {/if}
    </div>

    <div class="extras">
      <div class="field">
        <span class="label">{t('logoPicker.farbwelt')}</span>
        <div class="row wrap">
          {#each hueThemes as h}
            <button type="button" class="hue" class:active={(config.logoHue || 0) === h} onclick={() => (config.logoHue = h)}
              style="--h: {h}deg" title={h ? t('logoPicker.hueTitle', [h]) : t('logoPicker.original')} aria-label={h ? t('logoPicker.hueLabel', [h]) : t('logoPicker.originalColors')}>
              {#if !h}<span>A</span>{/if}
            </button>
          {/each}
        </div>
      </div>
      <div class="field">
        <span class="label">{t('logoPicker.anordnung')}</span>
        <button type="button" class="btn sm" onclick={() => (config.logoSeed = Math.floor(Math.random() * 100000))}>{t('logoPicker.neu_wuerfeln')}</button>
        <span class="hint">{t('logoPicker.verteilt_sterne_herzen_wellen_co')}</span>
      </div>
      <div class="field grow-2">
        <label class="label" for="tagline">{t('logoPicker.slogan')} <span class="muted">{t('logoPicker.optional')}</span></label>
        <input id="tagline" class="input" maxlength="40" bind:value={config.logoTagline} placeholder={t('logoPicker.z_b_filme_fuer_die')} />
      </div>
      <div class="field">
        <Toggle bind:checked={config.logoNumber} label={t('logoPicker.kanalnummer_im_logo')} hint={t('logoPicker.kleine_plakette_oben_links')} />
      </div>
      <div class="field grow-2">
        <Toggle checked={!!config.logoHoliday} onchange={(v) => (config.logoHoliday = v)} label={t('logoPicker.feiertags_accessoires')}
          hint={seasonal
            ? t('logoPicker.holidaySeasonal')
            : t('logoPicker.holidayHint')} />
        {#if config.logoHoliday}
          <div class="row wrap holi">
            <span class="tiny muted">{t('logoPicker.previewLabel')}</span>
            <select class="input mini" bind:value={holidayPreview}>
              {#each holidays as h}<option value={h.value}>{h.label}</option>{/each}
            </select>
          </div>
        {/if}
      </div>
    </div>
  {/if}
  <input bind:this={fileInput} type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden
    onchange={(e) => { const f = e.currentTarget.files?.[0]; if (f) onupload?.(f); e.currentTarget.value = '' }} />
</div>

<style>
  .extras { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 1rem 1.25rem; margin-top: 1.1rem; padding-top: 1.1rem; border-top: 1px solid var(--border); align-items: start; }
  .extras .field { gap: 0.35rem; align-items: flex-start; }
  .grow-2 { grid-column: span 1; }
  .hue { width: 28px; height: 28px; border-radius: 8px; border: 2px solid var(--border-strong); cursor: pointer; padding: 0;
    background: linear-gradient(135deg, #e5a00d, #ef4444 50%, #8b5cf6); filter: hue-rotate(var(--h)); color: white; font-weight: 800; font-size: 0.75rem; }
  .hue.active { border-color: white; box-shadow: 0 0 0 2px var(--accent); }
  .holi { margin-top: 0.3rem; gap: 0.4rem; }
  .mini { width: auto; padding: 0.25rem 2rem 0.25rem 0.5rem; font-size: 0.82rem; }
  .gallery { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 0.6rem; }
  .style { border: 2px solid transparent; background: var(--bg-2); border-radius: var(--radius-sm); padding: 0.4rem; cursor: pointer; color: var(--text-2); font: inherit; font-size: 0.8rem; font-weight: 600; display: flex; flex-direction: column; gap: 0.3rem; transition: border-color 0.15s, transform 0.15s; }
  .style:hover { transform: translateY(-2px); color: var(--text); }
  .style.active { border-color: var(--accent); color: var(--text); }
  .label-row { display: inline-flex; align-items: center; gap: 0.3rem; justify-content: center; }
  .logo-frame { aspect-ratio: 16 / 9; border-radius: 6px; display: grid; place-items: center; overflow: hidden;
    background: radial-gradient(circle at 50% 40%, #2a2c38, #15161c); }
  .logo-frame img { width: 100%; height: 100%; object-fit: contain; }
  .logo-frame.big { width: 240px; }
  .custom { display: flex; gap: 1rem; align-items: center; flex-wrap: wrap; }
  .colors { margin-top: 0.9rem; gap: 0.4rem; }
  .nomb { margin: 0 0.4rem 0 0; }
  .swatch { width: 26px; height: 26px; border-radius: 50%; border: 2px solid var(--border-strong); background: var(--sw); cursor: pointer; padding: 0; position: relative; overflow: hidden; }
  .swatch.active { border-color: white; box-shadow: 0 0 0 2px var(--accent); }
  .custom-color { background: conic-gradient(from 90deg, #fff, #888, #fff); }
  .custom-color input { position: absolute; inset: -6px; width: 40px; height: 40px; opacity: 0; cursor: pointer; }
  .drop { display: flex; flex-direction: column; gap: 0.35rem; color: var(--muted); border: 2px dashed var(--border-strong); background: var(--bg-2); text-align: center; padding: 0.4rem; }
  .upload-tile:hover .drop, .upload-tile.over .drop { border-color: var(--accent); color: var(--accent-2); }
</style>
