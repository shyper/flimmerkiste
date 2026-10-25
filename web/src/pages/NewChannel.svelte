<script>
  import { t } from '../lib/i18n.svelte.js'
  import Icon from '../components/Icon.svelte'
  import { kinds, templates, emptyConfig, pending } from '../lib/templates.js'
  import { navigate } from '../lib/router.svelte.js'
  import { get } from '../lib/api.js'

  function start(kind, name = '', config = null) {
    pending.draft = { kind, name, config: config || emptyConfig(kind) }
    navigate(`/channels/new/${kind}`)
  }

  async function useTemplate(t) {
    if (t.preset) {
      const cfg = await get('/api/presets/seasonal')
      cfg.logoStyle = 'auto'
      return start(t.kind, t.name, cfg)
    }
    start(t.kind, t.name, t.config())
  }
</script>

<div class="page">
  <div class="page-head">
    <div>
      <h1>{t('newChannel.neuer_sender')}</h1>
      <p>{t('newChannel.wie_soll_dein_sender_sein')}</p>
    </div>
  </div>

  <div class="kinds">
    {#each kinds as k}
      <button class="kind" style="--c: {k.color}" onclick={() => start(k.kind)}>
        <span class="ic"><Icon name={k.icon} size={26} /></span>
        <span class="title">{k.title}</span>
        <span class="text">{k.text}</span>
        <span class="go">{t('newChannel.los_geht_s')} <Icon name="right" size={14} /></span>
      </button>
    {/each}
  </div>

  <h2 class="tpl-title">{t('newChannel.oder_mit_einer_vorlage_starten')}</h2>
  <div class="templates">
    {#each templates as tpl}
      <button class="tpl" onclick={() => useTemplate(tpl)}>
        <span class="emoji">{tpl.emoji}</span>
        <span class="grow">
          <b>{tpl.name}</b>
          <span class="muted small block">{tpl.text}</span>
        </span>
        <Icon name="right" size={16} />
      </button>
    {/each}
  </div>
</div>

<style>
  .kinds { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 1rem; }
  .kind { text-align: left; display: flex; flex-direction: column; gap: 0.6rem; padding: 1.4rem; border-radius: 16px; border: 1px solid var(--border); background: linear-gradient(160deg, color-mix(in srgb, var(--c) 12%, transparent), transparent 55%), var(--surface); color: var(--text); font: inherit; cursor: pointer; transition: transform 0.15s, border-color 0.15s, box-shadow 0.15s; }
  .kind:hover { transform: translateY(-3px); border-color: color-mix(in srgb, var(--c) 60%, transparent); box-shadow: 0 12px 30px color-mix(in srgb, var(--c) 15%, transparent); }
  .ic { width: 48px; height: 48px; border-radius: 12px; display: grid; place-items: center; background: color-mix(in srgb, var(--c) 18%, transparent); color: var(--c); }
  /* a button holds phrasing content only: spans styled like the heading and the text */
  .title { font-size: 1.05rem; font-weight: 700; line-height: 1.25; letter-spacing: -0.01em; }
  .text { color: var(--text-2); font-size: 0.9rem; flex: 1; }
  .go { color: var(--c); font-weight: 700; font-size: 0.85rem; display: inline-flex; align-items: center; gap: 0.3rem; }
  .tpl-title { margin: 2.5rem 0 1rem; }
  .templates { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 0.6rem; }
  .tpl { display: flex; align-items: center; gap: 0.9rem; padding: 0.85rem 1rem; border-radius: var(--radius); border: 1px solid var(--border); background: var(--surface); color: var(--text); font: inherit; text-align: left; cursor: pointer; transition: background 0.15s, border-color 0.15s; }
  .tpl:hover { background: var(--surface-2); border-color: var(--border-strong); }
  .emoji { font-size: 1.7rem; }
  .block { display: block; }
</style>
