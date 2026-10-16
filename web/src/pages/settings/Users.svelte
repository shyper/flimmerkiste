<script>
  import { onMount } from 'svelte'
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../../components/Icon.svelte'
  import Toggle from '../../components/Toggle.svelte'
  import Modal from '../../components/Modal.svelte'
  import { get, put, del } from '../../lib/api.js'
  import { app, toast } from '../../lib/state.svelte.js'
  import { fmtAgo } from '../../lib/format.js'

  let { s } = $props()
  let users = $state([])
  let removing = $state(null)
  let confirmOpen = $state(false)

  onMount(async () => {
    users = await get('/api/users').catch(() => [])
  })

  async function setRole(u, role) {
    try {
      await put(`/api/users/${u.id}`, { role })
      u.role = role
      toast(t('settings.users.roleChanged', [u.username, role === 'admin' ? t('role.admin') : t('role.viewer')]))
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  async function remove() {
    const u = removing
    confirmOpen = false
    try {
      await del(`/api/users/${u.id}`)
      users = users.filter((x) => x.id !== u.id)
    } catch (e) {
      toast(e.message, 'error')
    }
  }
</script>

<div class="stack users">
  <Toggle bind:checked={s.allowSharedUsers} label={t('settings.users.shared')} hint={t('settings.users.sharedHint')} />
  <ul>
    {#each users as u (u.id)}
      <li>
        {#if u.thumb}<img src={u.thumb} alt="" />{:else}<span class="avatar"><Icon name="user" size={16} /></span>{/if}
        <div class="grow">
          <b>{u.username}</b> {#if u.owner}<span class="chip accent">{t('settings.users.owner')}</span>{/if}
          <div class="tiny muted">{t('settings.users.lastLogin', [fmtAgo(u.lastLogin)])}</div>
        </div>
        {#if !u.owner && u.id !== app.user.id}
          <select class="input role" value={u.role} onchange={(e) => setRole(u, e.currentTarget.value)} aria-label={t('settings.users.role')}>
            <option value="viewer">{t('role.viewer')}</option>
            <option value="admin">{t('role.admin')}</option>
          </select>
          <button class="btn ghost icon sm" onclick={() => ((removing = u), (confirmOpen = true))} title={t('settings.remove')} aria-label={t('settings.remove')}><Icon name="trash" size={15} /></button>
        {:else}
          <span class="chip">{u.role === 'admin' ? t('role.admin') : t('role.viewer')}</span>
        {/if}
      </li>
    {/each}
  </ul>
</div>

<Modal bind:open={confirmOpen} title={t('settings.users.removeTitle')}>
  <p>{t('settings.users.removeText', [removing?.username || ''])}</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (confirmOpen = false)}>{t('common.cancel')}</button>
    <button class="btn danger" onclick={remove}><Icon name="trash" size={16} /> {t('settings.remove')}</button>
  {/snippet}
</Modal>

<style>
  .users ul { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .users li { display: flex; align-items: center; gap: 0.75rem; padding: 0.5rem 0.6rem; background: var(--bg-2); border-radius: var(--radius-sm); }
  .users img, .avatar { width: 34px; height: 34px; border-radius: 50%; display: grid; place-items: center; background: var(--surface-3); }
  .role { width: auto; }
</style>
