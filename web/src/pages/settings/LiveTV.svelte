<script>
  import { t, locale } from '../../lib/i18n.svelte.js'
  import Icon from '../../components/Icon.svelte'
  import Help from '../../components/Help.svelte'
  import TunerHelp from '../../components/TunerHelp.svelte'
  import LiveTVSetup from '../../components/LiveTVSetup.svelte'
  import { app } from '../../lib/state.svelte.js'

  let { s } = $props()
</script>

<div class="stack">
  <details class="setup" open={!s.guideReload}>
    <summary>{t('settings.livetv.setup')}</summary>
    {#if app.meta?.platform && app.meta.platform !== 'plex'}<LiveTVSetup />{:else}<TunerHelp />{/if}
  </details>
  <div class="field">
    <label class="label" for="fname">{t('settings.livetv.name')}</label>
    <input id="fname" class="input" bind:value={s.friendlyName} />
  </div>
  <div class="field">
    <label class="label" for="tuners">{t('settings.livetv.count')}</label>
    <input id="tuners" class="input num" type="number" min="1" max="16" bind:value={s.tunerCount} />
    <span class="hint">{t('settings.livetv.countHint')}</span>
  </div>
  <div class="field">
    <label class="label" for="prefix">{t('settings.livetv.prefix')} <span class="muted">{t('settings.optionalTag')}</span></label>
    <input id="prefix" class="input num-wide" bind:value={s.epgPrefix} placeholder="fk-" maxlength="20" />
    <span class="hint">{t('settings.livetv.prefixHint', [`${s.epgPrefix || ''}1`])}</span>
    <Help>{t('settings.livetv.prefixMore')}</Help>
  </div>
  <div class="field">
    <span class="label">{t('settings.livetv.guide')}</span>
    <span class="hint">{t('settings.livetv.guideHint')}</span>
    {#if s.guideReload}
      <span class="hint">
        {#if s.guideReload.ok}<Icon name="check" size={13} class="ok" />{:else}<Icon name="alert" size={13} class="bad" />{/if}
        {new Date(s.guideReload.at * 1000).toLocaleString(locale(), { dateStyle: 'short', timeStyle: 'short' })}: {s.guideReload.message}
      </span>
    {/if}
  </div>
</div>

<style>
  .num { width: 110px; }
  .num-wide { width: 200px; }
  .setup { background: var(--bg-2); border-radius: var(--radius-sm); padding: 0.6rem 0.9rem; }
  .setup summary { cursor: pointer; font-weight: 600; }
  .stack :global(.ok) { color: var(--green); }
  .stack :global(.bad) { color: var(--red); }
</style>
