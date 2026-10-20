<script>
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../../components/Icon.svelte'
  import Segmented from '../../components/Segmented.svelte'
  import { put, post } from '../../lib/api.js'

  let { s } = $props()
  let testing = $state(false)
  let result = $state(null)

  async function test() {
    testing = true
    result = null
    try {
      await put('/api/settings', $state.snapshot(s))
      result = { ok: true, picks: await post('/api/ai/test') }
    } catch (e) {
      result = { ok: false, error: e.message }
    } finally {
      testing = false
    }
  }

  const modelHint = $derived(s.aiProvider === 'anthropic'
    ? t('settings.ai.modelHintAnthropic', [s.aiDefaultModel])
    : t('settings.ai.modelHintOpenAI'))
</script>

<div class="stack">
  <Segmented bind:value={s.aiProvider} options={[
    { value: '', label: t('settings.off') },
    { value: 'anthropic', label: 'Claude (Anthropic)' },
    { value: 'openai', label: t('settings.ai.openai') },
  ]} />
  {#if s.aiProvider}
    {#if s.aiProvider === 'openai'}
      <div class="field">
        <label class="label" for="base">{t('settings.ai.endpoint')}</label>
        <input id="base" class="input" bind:value={s.aiBaseUrl} placeholder="https://api.openai.com/v1" />
        <span class="hint">{t('settings.ai.endpointHint')}</span>
      </div>
    {/if}
    <div class="field">
      <label class="label" for="key">{t('settings.ai.key')}</label>
      <input id="key" class="input" type="password" bind:value={s.aiKey} placeholder={s.aiProvider === 'openai' ? t('settings.ai.keyOllama') : 'sk-ant-…'} autocomplete="off" />
    </div>
    <div class="field">
      <label class="label" for="model">{t('settings.ai.model')}</label>
      <input id="model" class="input" bind:value={s.aiModel} placeholder={s.aiProvider === 'anthropic' ? s.aiDefaultModel : t('settings.ai.modelName')} />
      <span class="hint">{modelHint}</span>
    </div>
    <div class="field">
      <label class="label" for="cands">{t('settings.ai.candidates')}</label>
      <input id="cands" class="input num" type="number" min="50" max="5000" step="50" bind:value={s.aiCandidates} />
      <span class="hint">{t('settings.ai.candidatesHint')}</span>
    </div>
    <div class="row">
      <button class="btn" onclick={test} disabled={testing}>
        {#if testing}<span class="spinner"></span>{:else}<Icon name="wand" size={16} />{/if} {t('settings.ai.test')}
      </button>
    </div>
    {#if result?.ok}
      <div class="alert info small stack">
        <b>{t('settings.ai.works')}</b>
        {#each result.picks as p}<div>🎬 <b>{p.title}</b> – {p.reason}</div>{/each}
      </div>
    {:else if result}
      <div class="alert error small">{result.error}</div>
    {/if}
  {/if}
</div>
