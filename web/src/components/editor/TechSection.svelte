<script>
  // Technology: how the stream is made, the logo in the picture, the credits.
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../Icon.svelte'
  import Toggle from '../Toggle.svelte'
  import Segmented from '../Segmented.svelte'
  import { app, toast } from '../../lib/state.svelte.js'

  let { ch = $bindable(), currentLogo = '' } = $props()

  let creditsVersion = $state(0)
  // "default" follows the global setting (Einstellungen → Streaming).
  const effectiveTranscode = $derived(ch.config.transcode === 'default' ? app.meta?.defaultTranscode || 'copy' : ch.config.transcode)
  const effectiveCredits = $derived(ch.config.credits === 'default' ? app.meta?.defaultCredits || 'off' : ch.config.credits)
  function toggleWatermark(on) {
    ch.config.watermark.enabled = on
    if (on && effectiveTranscode !== 'full') {
      ch.config.transcode = 'full'
      toast(t('channelEditor.fullForWatermark'))
    }
  }
  const wmStyle = $derived.by(() => {
    const w = ch.config.watermark
    if (!w) return ''
    const v = w.position?.startsWith('b') ? 'bottom' : 'top'
    const h = w.position?.endsWith('l') ? 'left' : 'right'
    const tone = { gray: '; filter: grayscale(1)', white: '; filter: brightness(0) invert(1)' }[w.tone] || ''
    return `${v}: 4%; ${h}: 3%; width: ${w.size}%; opacity: ${w.opacity / 100}${tone}`
  })
  const transcodeModes = [
    { value: 'copy', cpu: 1, get title() { return t('transcode.copy') }, get text() { return t('transcode.copy.text') } },
    { value: 'audio', cpu: 2, get title() { return t('transcode.audio') }, get text() { return t('transcode.audio.text') } },
    { value: 'full', cpu: 3, get title() { return t('transcode.full') }, get text() { return t('transcode.full.text') } },
  ]
  const global = $derived(transcodeModes.find((m) => m.value === (app.meta?.defaultTranscode || 'copy')))
</script>

<section class="card" id="sec-tech">
  <div class="section-title"><Icon name="cpu" /><h2>{t('channelEditor.technik')}</h2></div>
  <div class="modes">
    <label class="mode" class:active={ch.config.transcode === 'default'}>
      <input type="radio" name="tc" value="default" bind:group={ch.config.transcode} />
      <div class="row"><b class="grow">{t('channelEditor.standard')}</b>
        <span class="cpu" title={t('channelEditor.cpu_last')}>{#each [1, 2, 3] as n}<i class:on={n <= global.cpu} class:hot={global.cpu === 3}></i>{/each}</span>
      </div>
      <span class="small muted">{t('channelEditor.wie_in_den_einstellungen_festgelegt', [global.title])}</span>
    </label>
    {#each transcodeModes as m}
      <label class="mode" class:active={ch.config.transcode === m.value}>
        <input type="radio" name="tc" value={m.value} bind:group={ch.config.transcode} />
        <div class="row"><b class="grow">{m.title}</b>
          <span class="cpu" title={t('channelEditor.cpu_last')}>{#each [1, 2, 3] as n}<i class:on={n <= m.cpu} class:hot={m.cpu === 3}></i>{/each}</span>
        </div>
        <span class="small muted">{m.text}</span>
      </label>
    {/each}
  </div>
  <div class="wm">
    <Toggle checked={ch.config.watermark.enabled} onchange={toggleWatermark} label={t('channelEditor.senderlogo_im_bild_einblenden')}
      hint={t('channelEditor.wie_im_echten_fernsehen_benoetigt')} />
    {#if ch.config.watermark.enabled}
      {#if effectiveTranscode !== 'full'}
        <div class="alert warn small"><Icon name="alert" size={15} /> {t('channelEditor.wird_nur_im_modus_vollstaendig')}</div>
      {/if}
      <div class="wm-grid">
        <div class="screen" style="--c: {ch.color || 'var(--accent)'}">
          <img class="wm-logo" style={wmStyle} alt=""
            src={currentLogo} />
        </div>
        <div class="stack">
          <div>
            <span class="label">{t('channelEditor.position')}</span>
            <Segmented bind:value={ch.config.watermark.position} size="sm" options={[
              { value: 'tl', label: '↖' }, { value: 'tr', label: '↗' }, { value: 'bl', label: '↙' }, { value: 'br', label: '↘' },
            ]} />
          </div>
          <div>
            <span class="label">{t('channelEditor.logoFarbe')}</span>
            <Segmented bind:value={ch.config.watermark.tone} size="sm" options={[
              { value: 'color', label: t('channelEditor.logoFarbe.color') }, { value: 'gray', label: t('channelEditor.logoFarbe.gray') },
              { value: 'white', label: t('channelEditor.logoFarbe.white') },
            ]} />
          </div>
          <label class="field">
            <span class="label">{t('channelEditor.groesse', [ch.config.watermark.size])}</span>
            <input type="range" min="6" max="30" bind:value={ch.config.watermark.size} />
          </label>
          <label class="field">
            <span class="label">{t('channelEditor.deckkraft', [ch.config.watermark.opacity])}</span>
            <input type="range" min="20" max="100" step="5" bind:value={ch.config.watermark.opacity} />
          </label>
        </div>
      </div>
    {/if}
  </div>
  <div class="wm">
    <div>
      <span class="label">{t('channelEditor.abspann')}</span>
      <Segmented bind:value={ch.config.credits} size="sm" options={[
        { value: 'default', label: app.meta?.defaultCredits === 'squeeze' ? t('credits.defaultSqueeze') : t('credits.defaultOff') },
        { value: 'off', label: t('credits.off') }, { value: 'squeeze', label: t('credits.squeeze') },
      ]} />
      <p class="hint">{t('channelEditor.das_bild_schrumpft_beim_abspann')}</p>
    </div>
    {#if effectiveCredits === 'squeeze'}
      {#if effectiveTranscode !== 'full'}
        <div class="alert warn small"><Icon name="alert" size={15} /> {t('channelEditor.wirkt_nur_im_modus_vollstaendig')}</div>
      {/if}
      <div class="card-preview">
        {#if ch.id}
          <img src="/api/channels/{ch.id}/credits?v={creditsVersion}" alt={t('channelEditor.infotafel_beim_abspann')} />
          <button class="btn ghost sm" onclick={() => creditsVersion++}><Icon name="refresh" size={14} /> {t('channelEditor.aktualisieren')}</button>
        {:else}
          <p class="hint">{t('channelEditor.die_vorschau_erscheint_nach_dem_2')}</p>
        {/if}
      </div>
    {/if}
  </div>
</section>

<style>
  .modes { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 0.6rem; }
  .mode { display: flex; flex-direction: column; gap: 0.35rem; padding: 0.9rem; border-radius: var(--radius-sm); border: 2px solid var(--border); background: var(--bg-2); cursor: pointer; }
  .mode.active { border-color: var(--accent); }
  .mode input { display: none; }
  .cpu { display: inline-flex; gap: 3px; }
  .cpu i { width: 6px; height: 12px; border-radius: 2px; background: var(--surface-3); }
  .cpu i.on { background: var(--green); }
  .cpu i.on.hot { background: var(--red); }
  .card-preview { display: flex; flex-direction: column; gap: 0.4rem; align-items: flex-start; }
  .card-preview img { width: 100%; max-width: 520px; aspect-ratio: 16 / 9; border-radius: var(--radius-sm); border: 1px solid var(--border); background: var(--surface-3); }
  .wm { margin-top: 1.25rem; padding-top: 1.25rem; border-top: 1px solid var(--border); display: flex; flex-direction: column; gap: 0.9rem; }
  .wm-grid { display: grid; grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr); gap: 1.25rem; align-items: start; }
  .screen { position: relative; aspect-ratio: 16 / 9; border-radius: var(--radius-sm); overflow: hidden; border: 1px solid var(--border);
    background: linear-gradient(160deg, #3b4a6b, #1d2433 45%, #4a3328), radial-gradient(circle at 30% 70%, rgba(255,255,255,0.08), transparent 50%); }
  .screen::after { content: 'Filmszene'; position: absolute; inset: 0; display: grid; place-items: center; color: rgba(255,255,255,0.18); font-weight: 800; letter-spacing: 0.2em; text-transform: uppercase; font-size: 0.8rem; }
  .wm-logo { position: absolute; z-index: 1; height: auto; transition: all 0.2s; }
  input[type='range'] { accent-color: var(--accent); width: 100%; }
  @media (max-width: 640px) { .wm-grid { grid-template-columns: 1fr; } }
</style>
