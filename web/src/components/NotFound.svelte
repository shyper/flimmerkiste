<script>
  import { t } from '../lib/i18n.svelte.js'
  import { link } from '../lib/router.svelte.js'
</script>

<div class="page empty"><h2>{t('app.seite_nicht_gefunden')}</h2><p><a href="/" use:link>{t('app.zur_uebersicht')}</a></p></div>
