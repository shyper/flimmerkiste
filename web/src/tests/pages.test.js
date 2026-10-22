// Snapshots of every page with recorded API answers (fixtures/api.json, made by
// FLIMMERKISTE_WEB_FIXTURES=1 go test -run TestWebFixtures ./internal/server/), at the
// recorded time in Berlin. They catch any change of text or markup; after an
// intended change, review and accept the new snapshots with `npx vitest run -u`.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/svelte'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import App from '../App.svelte'
import Login from '../pages/Login.svelte'
import Setup from '../pages/Setup.svelte'
import Channels from '../pages/Channels.svelte'
import Guide from '../pages/Guide.svelte'
import Library from '../pages/Library.svelte'
import History from '../pages/History.svelte'
import Settings from '../pages/Settings.svelte'
import NewChannel from '../pages/NewChannel.svelte'
import ChannelEditor from '../pages/ChannelEditor.svelte'
import { app } from '../lib/state.svelte.js'
import { setLang, i18n } from '../lib/i18n.svelte.js'
import { route } from '../lib/router.svelte.js'

const fixtures = JSON.parse(readFileSync(resolve(process.cwd(), 'src/tests/fixtures/api.json'), 'utf8'))
const servers = [
  { id: 'abc', name: 'Wohnzimmer-Server', suggested: 'http://127.0.0.1:32400', connections: ['http://127.0.0.1:32400', 'http://192.168.1.20:32400'] },
  { id: 'def', name: 'Büro', suggested: '', connections: ['https://10-0-0-5.plex.direct:32400'] },
]

const stubs = {
  '/api/preview': { count: 3, movies: 3, episodes: 0, shows: 0, trailers: 0, durationMs: 1, items: [] },
  '/api/library/search': { movies: [], shows: [] },
  '/api/tokens': { endpoint: 'http://192.168.1.10:8080/mcp', scopes: ['read', 'library', 'write', 'delete'], tokens: [
    { id: 1, userId: 1, username: 'owner', name: 'Claude Desktop', scopes: ['read', 'library'], createdAt: 1760000000, lastUsed: 0 },
  ] },
}

function mockApi(extra = {}) {
  globalThis.fetch = vi.fn(async (url) => {
    const path = String(url).split('?')[0]
    let body = extra[path] ?? fixtures.api[path] ?? stubs[path]
    if (body === undefined && path.startsWith('/api/channels/') && path.endsWith('/error')) body = {}
    if (body === undefined) body = {}
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
}

const settle = () => new Promise((r) => setTimeout(r, 1500))

// normalize writes the DOM as the browser shows it: attributes sorted,
// runs of whitespace collapsed, comments and Svelte's scoping classes left out.
function normalize(node) {
  if (node.nodeType === 3) return node.data.replace(/\s+/g, ' ')
  if (node.nodeType !== 1) return ''
  const attrs = [...node.attributes]
    .map((a) => `${a.name}="${a.name === 'class' ? a.value.replace(/\s*svelte-[a-z0-9]+/g, '').trim().replace(/\s+/g, ' ') : a.value}"`)
    .sort().join(' ')
  const inner = [...node.childNodes].map(normalize).join('')
  return `<${node.localName}${attrs ? ' ' + attrs : ''}>${inner}</${node.localName}>`
}

async function snapIn(lang, name, Component, props = {}) {
  const { container } = render(Component, { props })
  await settle()
  const html = [...container.childNodes].map(normalize).join('').replace(/ {2,}/g, ' ').replace(/></g, '>\n<')
  await expect(html).toMatchFileSnapshot(`__snapshots__/${lang === 'de' ? '' : lang + '/'}${name}.html`)
}

beforeEach(() => {
  setLang('de')
  vi.useFakeTimers({ now: fixtures.now, toFake: ['Date'] })
  mockApi()
  app.status = fixtures.api['/api/auth/status']
  app.user = app.status.user
  app.meta = fixtures.api['/api/meta']
  app.facets = fixtures.api['/api/library/facets']
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

function pages(lang) {
  it('Rahmen', async () => {
    app.ready = true
    mockApi({ '/api/auth/status': { ...fixtures.api['/api/auth/status'], language: lang } })
    await snap('app', App)
  })
  it('Anmeldung, erste', async () => {
    app.status = { claimed: false, serverLinked: false }
    await snap('login-first', Login)
  })
  it('Anmeldung', async () => {
    await snap('login', Login)
  })
  it('Anmeldung Jellyfin', async () => {
    app.status = { ...app.status, platform: 'jellyfin', serverName: 'Wohnzimmer' }
    await snap('login-jellyfin', Login)
  })
  it('Einrichtung', async () => {
    app.status = { ...app.status, serverLinked: false }
    mockApi({ '/api/setup/servers': servers })
    await snap('setup', Setup)
  })
  it('Sender', async () => snap('channels', Channels))
  it('Programm', async () => snap('guide', Guide))
  it('Bibliothek', async () => snap('library', Library))
  it('Verlauf', async () => {
    const at = (h) => fixtures.now - h * 3600e3
    mockApi({ '/api/history': [
      { id: 3, channelId: 1, channelName: 'Komödien', start: at(1), stop: at(-0.5), ratingKey: '7', type: 'movie', title: 'Die Ärzte', year: 1999,
        watched: true, failed: false, times: 3, previous: at(49) },
      { id: 2, channelId: 2, channelName: 'Krimi', start: at(2), stop: at(1), ratingKey: '8', type: 'episode', title: 'Lösegeld für einen Toten',
        showTitle: 'Columbo', season: 1, episode: 2, watched: true, failed: true, times: 1 },
      { id: 1, channelId: 1, channelName: 'Komödien', start: at(26), stop: at(24.5), ratingKey: '9', type: 'movie', title: 'Hot Fuzz', year: 2007,
        watched: false, failed: false, times: 1 },
    ] })
    await snap('history', History)
  })
  for (const section of ['server', 'livetv', 'playback', 'presentation', 'trailers', 'hardware', 'ai', 'users', 'mcp', 'general']) {
    it(`Einstellungen ${section}`, async () => {
      route.path = `/settings/${section}`
      await snap(`settings-${section}`, Settings)
      route.path = '/'
    })
  }
  it('Einstellungen trailers Jellyfin', async () => {
    app.meta = { ...app.meta, platform: 'jellyfin' }
    mockApi({
      '/api/meta': app.meta,
      '/api/settings': { ...fixtures.api['/api/settings'], tmdbKey: '••••••••', tmdbRegion: '', tmdbRegionAuto: 'DE', slateSeconds: 20,
        weatherPlace: { name: 'Hamburg', region: 'Hamburg, Deutschland', lat: 53.55, lon: 9.99 }, newsFeeds: 'https://www.tagesschau.de/index~rss2.xml' },
      '/api/trailers': { ...fixtures.api['/api/trailers'], stats: [{ origin: 'tmdb', language: '', titles: 17, count: 17 }],
        slates: { last: fixtures.now / 1000 - 3600, result: { titles: 17, trailers: 17 } },
        info: { weather: { last: fixtures.now / 1000 - 1200 }, news: { last: fixtures.now / 1000 - 300, count: 42, error: 'www.example.org: feed: HTTP 404' } } },
    })
    route.path = '/settings/trailers'
    await snap('settings-trailers-jellyfin', Settings)
    route.path = '/'
  })
  it('Neuer Sender', async () => snap('new-channel', NewChannel))
  it('Sender-Editor', async () => snap('editor', ChannelEditor, { id: '1' }))
  for (const kind of ['manual', 'smart', 'seasonal', 'ai']) {
    it(`Sender-Editor neu ${kind}`, async () => snap(`editor-new-${kind}`, ChannelEditor, { kind }))
  }
}

async function snap(name, Component, props = {}) {
  return snapIn(i18n.lang, name, Component, props)
}

describe('Seiten', () => pages('de'))

describe('Pages in English', () => {
  beforeEach(() => setLang('en'))
  pages('en')
})
