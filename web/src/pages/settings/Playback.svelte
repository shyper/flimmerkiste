<script>
  import { t } from '../../lib/i18n.svelte.js'
  import Icon from '../../components/Icon.svelte'
  import Help from '../../components/Help.svelte'
  import Segmented from '../../components/Segmented.svelte'
  import FullOnly from '../../components/FullOnly.svelte'

  let { s } = $props()

  // Audio languages in the order of preference, stored as ISO codes ("deu,ger,eng").
  const languages = [
    ['Deutsch', 'deu', 'ger'], ['English', 'eng'], ['Français', 'fra', 'fre'], ['Español', 'spa'], ['Italiano', 'ita'],
    ['Nederlands', 'nld', 'dut'], ['Português', 'por'], ['Polski', 'pol'], ['Svenska', 'swe'], ['Dansk', 'dan'],
    ['Norsk', 'nor', 'nob'], ['Suomi', 'fin'], ['Čeština', 'ces', 'cze'], ['Türkçe', 'tur'], ['Русский', 'rus'],
    ['日本語', 'jpn'], ['한국어', 'kor'], ['中文', 'zho', 'chi'],
  ]
  const byCode = Object.fromEntries(languages.flatMap((l) => l.slice(1).map((c) => [c, l])))
  // chosen: [{ name, codes }] in order; unknown codes stay as they are
  const chosen = $derived.by(() => {
    const out = []
    for (const code of (s.audioLanguages || '').split(',').map((c) => c.trim()).filter(Boolean)) {
      const l = byCode[code]
      const name = l ? l[0] : code
      if (!out.some((x) => x.name === name)) out.push({ name, codes: l ? l.slice(1) : [code] })
    }
    return out
  })
  const store = (list) => (s.audioLanguages = list.flatMap((x) => x.codes).join(','))
  function add(e) {
    const l = languages.find((x) => x[0] === e.currentTarget.value)
    e.currentTarget.value = ''
    if (l) store([...chosen, { name: l[0], codes: l.slice(1) }])
  }
  function move(i, d) {
    const list = [...chosen]
    const [x] = list.splice(i, 1)
    list.splice(i + d, 0, x)
    store(list)
  }
  const remove = (i) => store(chosen.filter((_, j) => j !== i))
</script>

<div class="stack">
  <div class="field">
    <span class="label">{t('settings.playback.languages')}</span>
    <ol class="langs">
      {#each chosen as l, i (l.name)}
        <li>
          <span class="grow">{i + 1}. {l.name} <span class="tiny muted">{l.codes.join(', ')}</span></span>
          <button class="btn ghost icon sm" onclick={() => move(i, -1)} disabled={i === 0} aria-label={t('settings.playback.up')}><Icon name="up" size={14} /></button>
          <button class="btn ghost icon sm" onclick={() => move(i, 1)} disabled={i === chosen.length - 1} aria-label={t('settings.playback.down')}><Icon name="down" size={14} /></button>
          <button class="btn ghost icon sm" onclick={() => remove(i)} aria-label={t('settings.remove')}><Icon name="x" size={14} /></button>
        </li>
      {/each}
    </ol>
    <select class="input auto" onchange={add} aria-label={t('settings.playback.addLanguage')}>
      <option value="">{t('settings.playback.addLanguage')}</option>
      {#each languages.filter((l) => !chosen.some((c) => c.name === l[0])) as l}<option value={l[0]}>{l[0]}</option>{/each}
    </select>
    <span class="hint">{t('settings.playback.languagesHint')}</span>
  </div>
  <div>
    <span class="label">{t('settings.playback.transcode')}</span>
    <Segmented bind:value={s.defaultTranscode} size="sm" options={[
      { value: 'copy', label: t('transcode.copy') }, { value: 'audio', label: t('transcode.audio') }, { value: 'full', label: t('transcode.full') },
    ]} />
    <span class="hint">{t('settings.playback.transcodeHint')}</span>
    <Help>{t('transcode.copy')}: {t('transcode.copy.text')} {t('transcode.audio')}: {t('transcode.audio.text')} {t('transcode.full')}: {t('transcode.full.text')}</Help>
  </div>
  <div>
    <span class="label">{t('settings.playback.resolution')}<FullOnly /></span>
    <Segmented bind:value={s.normalizeRes} size="sm" options={[{ value: 720, label: t('settings.playback.res720') }, { value: 1080, label: '1080p' }]} />
    <span class="hint">{t('settings.playback.resolutionHint')}</span>
  </div>
  <div class="field">
    <label class="label" for="buffer">{t('settings.playback.buffer')}</label>
    <div class="row"><input id="buffer" class="input num" type="number" min="2" max="30" bind:value={s.streamBuffer} /> <span class="muted small">{t('settings.seconds')}</span></div>
    <span class="hint">{t('settings.playback.bufferHint')}</span>
  </div>
</div>

<style>
  .num { width: 110px; }
  .auto { width: auto; }
  .langs { list-style: none; padding: 0; margin: 0 0 0.4rem; display: flex; flex-direction: column; gap: 0.25rem; max-width: 420px; }
  .langs li { display: flex; align-items: center; gap: 0.3rem; padding: 0.3rem 0.5rem; background: var(--bg-2); border-radius: var(--radius-sm); }
</style>
