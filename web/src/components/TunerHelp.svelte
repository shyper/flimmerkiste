<script>
  import { t } from '../lib/i18n.svelte.js'
  import Icon from './Icon.svelte'
  import { app, copyText } from '../lib/state.svelte.js'

  const tuner = $derived(app.meta?.tuner || { url: location.origin, epg: location.origin + '/epg.xml' })

  const copy = copyText
</script>

<ol class="steps">
  <li>
    <b>{t('tunerHelp.plex_web_oeffnen')}</b> → <i>{t('tunerHelp.einstellungen')}</i> → <i>{t('tunerHelp.live_tv_dvr')}</i> → <i>{t('tunerHelp.dvr_einrichten')}</i>.
  </li>
  <li> {t('tunerHelp.wird_kein_geraet_gefunden_auf')} <i>{t('tunerHelp.netzwerkadresse_manuell_eingeben')}</i> {t('tunerHelp.klicken_und_diese_adresse_eintragen')} <div class="copy"><code>{tuner.url}</code><button class="btn sm" onclick={() => copy(tuner.url)}><Icon name="copy" size={14} /> {t('tunerHelp.kopieren')}</button></div>
  </li>
  <li>{t('tunerHelp.plex_zeigt_die_sender_an')} <i>{t('tunerHelp.weiter')}</i> {t('tunerHelp.bestaetigen')}</li>
  <li> {t('tunerHelp.beim_programmfuehrer')} <i>{t('tunerHelp.xmltv_programmfuehrer_verwenden')}</i> {t('tunerHelp.waehlen_und_diese_adresse_angeben')} <div class="copy"><code>{tuner.epg}</code><button class="btn sm" onclick={() => copy(tuner.epg)}><Icon name="copy" size={14} /> {t('tunerHelp.kopieren')}</button></div>
  </li>
  <li>{t('tunerHelp.fertig_die_sender_erscheinen_in')} <i>{t('tunerHelp.live_tv')}</i>{t('tunerHelp.inklusive_logos_und_programm')}</li>
</ol>
<p class="hint">{t('tunerHelp.tipp_wenn_du_neue_sender')} <i>{t('tunerHelp.live_tv_dvr_kanaele_durchsuchen')}</i> {t('tunerHelp.die_liste_aktualisieren_das_programm')}</p>

<style>
  .steps { margin: 0; padding-left: 1.3rem; display: flex; flex-direction: column; gap: 0.8rem; }
  .steps li { padding-left: 0.3rem; color: var(--text-2); }
  .steps li::marker { color: var(--accent); font-weight: 800; }
  .steps b, .steps i { color: var(--text); font-style: normal; }
  .copy { display: flex; align-items: center; gap: 0.5rem; margin-top: 0.4rem; background: var(--bg-2); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 0.35rem 0.35rem 0.35rem 0.75rem; }
  code { flex: 1; font-size: 0.9rem; color: var(--accent-2); overflow-wrap: anywhere; }
</style>
