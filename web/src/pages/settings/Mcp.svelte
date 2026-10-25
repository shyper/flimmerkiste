<script>
  import { onMount } from 'svelte'
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../../components/Icon.svelte'
  import Modal from '../../components/Modal.svelte'
  import Segmented from '../../components/Segmented.svelte'
  import { get, post, del } from '../../lib/api.js'
  import { toast, copyText } from '../../lib/state.svelte.js'
  import { fmtAgo } from '../../lib/format.js'

  let data = $state({ tokens: [], scopes: [], endpoint: '' })
  let name = $state('')
  let scopes = $state(['read', 'library'])
  let creating = $state(false)
  let created = $state(null) // the new token, shown once
  let client = $state('claude')
  let revoking = $state(null)
  let confirmOpen = $state(false)

  onMount(async () => {
    data = await get('/api/tokens').catch(() => data)
  })

  // The guide shows real values once a token was just created, a placeholder before.
  const token = $derived(created || t('settings.mcp.tokenPlaceholder'))
  const header = $derived(`Bearer ${token}`) // value of the Authorization header
  const tokenUrl = $derived(`${data.endpoint}/${token}`) // for apps that cannot send a header
  const command = $derived(`claude mcp add --transport http flimmerkiste ${data.endpoint} --header "Authorization: Bearer ${token}"`)
  // The apps connect from the internet: an address of the home network will not do.
  const local = $derived(data.endpoint.startsWith('http://'))

  const clients = [
    { value: 'claude', get label() { return t('settings.mcp.client.claude') } },
    { value: 'claudeCode', label: 'Claude Code' },
    { value: 'editor', get label() { return t('settings.mcp.client.editor') } },
    { value: 'chatgpt', label: 'ChatGPT' },
  ]

  const copy = copyText

  function toggle(scope) {
    scopes = scopes.includes(scope) ? scopes.filter((x) => x !== scope) : [...scopes, scope]
  }

  async function create() {
    creating = true
    try {
      const res = await post('/api/tokens', { name, scopes })
      created = res.token
      data.tokens = [...data.tokens, res.info]
      name = ''
    } catch (e) {
      toast(e.message, 'error')
    } finally {
      creating = false
    }
  }

  async function revoke() {
    const tok = revoking
    confirmOpen = false
    try {
      await del(`/api/tokens/${tok.id}`)
      data.tokens = data.tokens.filter((x) => x.id !== tok.id)
    } catch (e) {
      toast(e.message, 'error')
    }
  }
</script>

{#snippet copyRow(text)}
  <div class="copy"><code>{text}</code><button class="btn sm" onclick={() => copy(text)}><Icon name="copy" size={14} /> {t('tunerHelp.kopieren')}</button></div>
{/snippet}

<div class="stack mcp">
  <h3>{t('settings.mcp.step1')}</h3>
  <div class="new">
    <div class="field">
      <label class="label" for="token-name">{t('settings.mcp.name')}</label>
      <input id="token-name" class="input" bind:value={name} placeholder={t('settings.mcp.namePlaceholder')} autocomplete="off" />
    </div>
    <fieldset class="scopes">
      <legend class="label">{t('settings.mcp.scopes')}</legend>
      {#each data.scopes as scope}
        <label class="scope">
          <input type="checkbox" checked={scopes.includes(scope)} onchange={() => toggle(scope)} />
          <span><b>{t(`settings.mcp.scope.${scope}`)}</b><span class="hint">{t(`settings.mcp.scope.${scope}.hint`)}</span></span>
        </label>
      {/each}
      <span class="hint">{t('settings.mcp.scopesTip')}</span>
    </fieldset>
    <div><button class="btn primary" onclick={create} disabled={creating || !name.trim() || !scopes.length}><Icon name="plus" size={16} /> {t('settings.mcp.create')}</button></div>
  </div>

  {#if created}
    <div class="created">
      <b>{t('settings.mcp.createdTitle')}</b>
      <span class="hint">{t('settings.mcp.createdHint')}</span>
      {@render copyRow(created)}
      <div><button class="btn sm" onclick={() => (created = null)}>{t('settings.mcp.done')}</button></div>
    </div>
  {/if}

  <h3>{t('settings.mcp.step2')}</h3>
  <p class="small">{t('settings.mcp.recommend')}</p>
  <Segmented bind:value={client} options={clients} />

  <div class="guide">
    {#if client === 'claude'}
      <ol>
        <li>{t('settings.mcp.claude.1')}</li>
        <li>{t('settings.mcp.claude.2')} {@render copyRow(data.endpoint)}</li>
        <li>{t('settings.mcp.claude.3')}</li>
        <li>{t('settings.mcp.claude.4')} {@render copyRow(header)}</li>
        <li>{t('settings.mcp.claude.5')}</li>
      </ol>
      <p class="hint">{t('settings.mcp.claude.note')}</p>
    {:else if client === 'claudeCode'}
      <ol>
        <li>{t('settings.mcp.claudeCode.1')} {@render copyRow(command)}</li>
      </ol>
      <p class="hint">{t('settings.mcp.claudeCode.note')}</p>
    {:else if client === 'editor'}
      <ol>
        <li>{t('settings.mcp.editor.1')} {@render copyRow(data.endpoint)}</li>
        <li>{t('settings.mcp.editor.2')} {@render copyRow(header)}</li>
      </ol>
      <p class="hint">{t('settings.mcp.editor.note')}</p>
    {:else}
      <ol>
        <li>{t('settings.mcp.chatgpt.1')}</li>
        <li>{t('settings.mcp.chatgpt.2')} {@render copyRow(tokenUrl)}</li>
        <li>{t('settings.mcp.chatgpt.3')}</li>
      </ol>
      <p class="hint warn"><Icon name="alert" size={14} /> {t('settings.mcp.chatgpt.note')}</p>
    {/if}
    {#if local && (client === 'claude' || client === 'chatgpt')}
      <p class="hint warn"><Icon name="alert" size={14} /> {t('settings.mcp.localWarn', [data.endpoint])}</p>
    {/if}
    {#if !created}
      <p class="hint">{t('settings.mcp.placeholderHint')}</p>
    {/if}
  </div>

  <h3>{t('settings.mcp.step3')}</h3>
  {#if data.tokens.length}
    <ul class="tokens">
      {#each data.tokens as tok (tok.id)}
        <li>
          <span class="avatar"><Icon name="key" size={16} /></span>
          <div class="grow">
            <b>{tok.name}</b> <span class="tiny muted">{tok.username}</span>
            <div class="chips">{#each tok.scopes as scope}<span class="chip">{t(`settings.mcp.scope.${scope}`)}</span>{/each}</div>
            <div class="tiny muted">{tok.lastUsed ? t('settings.mcp.lastUsed', [fmtAgo(tok.lastUsed)]) : t('settings.mcp.unused')}</div>
          </div>
          <button class="btn ghost icon sm" onclick={() => ((revoking = tok), (confirmOpen = true))} title={t('settings.mcp.revoke')} aria-label={t('settings.mcp.revoke')}><Icon name="trash" size={15} /></button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="small muted">{t('settings.mcp.none')}</p>
  {/if}
</div>

<Modal bind:open={confirmOpen} title={t('settings.mcp.revokeTitle')}>
  <p>{t('settings.mcp.revokeText', [revoking?.name || ''])}</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (confirmOpen = false)}>{t('common.cancel')}</button>
    <button class="btn danger" onclick={revoke}><Icon name="trash" size={16} /> {t('settings.mcp.revoke')}</button>
  {/snippet}
</Modal>

<style>
  h3 { margin: 0.6rem 0 0; font-size: 1rem; }
  .copy { display: flex; align-items: center; gap: 0.5rem; margin-top: 0.4rem; background: var(--bg-2); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 0.35rem 0.35rem 0.35rem 0.75rem; }
  code { flex: 1; font-size: 0.9rem; color: var(--accent-2); overflow-wrap: anywhere; }
  .created { display: flex; flex-direction: column; gap: 0.5rem; padding: 0.8rem; border: 1px solid var(--accent); border-radius: var(--radius-sm); }
  .new { display: flex; flex-direction: column; gap: 0.8rem; padding: 0.8rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  .scopes { border: 0; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .scope { display: flex; align-items: flex-start; gap: 0.6rem; cursor: pointer; }
  .scope input { margin-top: 0.25rem; }
  .scope > span { display: flex; flex-direction: column; }
  .guide { display: flex; flex-direction: column; gap: 0.6rem; padding: 0.8rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  .guide ol { margin: 0; padding-left: 1.3rem; display: flex; flex-direction: column; gap: 0.7rem; color: var(--text-2); }
  .guide ol li::marker { color: var(--accent); font-weight: 800; }
  .guide .copy { background: var(--bg); }
  .guide p { margin: 0; }
  .warn :global(.icon) { vertical-align: -2px; color: var(--accent-2); }
  .tokens { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .tokens li { display: flex; align-items: center; gap: 0.75rem; padding: 0.5rem 0.6rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  .avatar { width: 34px; height: 34px; border-radius: 50%; display: grid; place-items: center; background: var(--surface-3); flex-shrink: 0; }
  .chips { display: flex; flex-wrap: wrap; gap: 0.3rem; margin: 0.2rem 0; }
</style>
