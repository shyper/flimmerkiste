// The editor must open every channel a live database may hold: configurations
// stored by older versions (internal/schedule/testdata/configs) as well as the
// same after the server migrated them (testdata/normalized). Rendering runs in
// jsdom; a missing value that a component binds to fails here instead of in the browser.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte'
import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import ChannelEditor from '../pages/ChannelEditor.svelte'
import Channels from '../pages/Channels.svelte'
import SourceEditor from '../components/SourceEditor.svelte'
import { app } from '../lib/state.svelte.js'
import { normalizeConfig } from '../lib/config.js'
import { setLang } from '../lib/i18n.svelte.js'

// Tests run from web/ (npm test); jsdom's import.meta.url is no file URL.
const testdata = resolve(process.cwd(), '../internal/schedule/testdata')
const read = (f) => JSON.parse(readFileSync(resolve(testdata, f), 'utf8'))
const load = (dir) =>
  readdirSync(resolve(testdata, dir)).filter((f) => f.endsWith('.json') && f !== 'fields.json')
    .map((f) => ({ name: `${dir}/${f}`, ...read(`${dir}/${f}`) }))
const stored = [...load('configs'), ...load('normalized')]
const fields = read('normalized/fields.json')

let errors
const onError = (e) => errors.push(e.error || e.reason || e)

function mockApi(channel) {
  globalThis.fetch = vi.fn(async (url) => {
    const u = String(url)
    let body = {}
    if (u.startsWith('/api/channels/')) body = channel
    else if (u.startsWith('/api/guide')) body = []
    else if (u.startsWith('/api/preview')) body = { count: 3, movies: 3, episodes: 0, shows: 0, trailers: 0, durationMs: 1, items: [] }
    else if (u.startsWith('/api/library/search')) body = { movies: [], shows: [] }
    else if (u.startsWith('/api/presets/seasonal')) body = { seasons: [], order: 'shuffle', transcode: 'copy' }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
}

beforeEach(() => {
  setLang('de')
  errors = []
  window.addEventListener('error', onError)
  window.addEventListener('unhandledrejection', onError)
  vi.spyOn(console, 'error').mockImplementation((...a) => errors.push(a.join(' ')))
  app.user = { role: 'admin' }
  app.meta = { fields, logoStyles: [{ id: 'auto', label: 'Automatisch' }], libraries: [{ id: '1', title: 'Filme' }], aiEnabled: true }
  app.facets = { genre: [{ value: 'Horror', count: 3 }], decade: [{ value: '1980', count: 2 }] }
})

afterEach(() => {
  cleanup()
  window.removeEventListener('error', onError)
  window.removeEventListener('unhandledrejection', onError)
  vi.restoreAllMocks()
})

describe('Sender-Editor öffnet gespeicherte Sender jeder Version', () => {
  for (const { name, kind, config } of stored) {
    it(name, async () => {
      mockApi({ id: 1, name: 'Testsender', number: 1, color: '', enabled: true, kind, config, autoLogo: true, logoUrl: '', missing: [] })
      render(ChannelEditor, { id: '1' })
      expect(await screen.findByRole('heading', { name: 'Sendezeiten & Pausen' })).toBeTruthy()
      expect(screen.getByDisplayValue('Testsender')).toBeTruthy()
      await new Promise((r) => setTimeout(r, 600)) // debounced preview requests
      expect(errors).toEqual([])
    })
  }
})

describe('Neue Sender', () => {
  for (const kind of ['manual', 'smart', 'seasonal', 'ai']) {
    it(kind, async () => {
      mockApi({})
      render(ChannelEditor, { kind })
      expect(await screen.findByRole('heading', { name: 'Sendezeiten & Pausen' })).toBeTruthy()
      expect(errors).toEqual([])
    })
  }
})

describe('Saison über den Jahreswechsel', () => {
  it.each(['day-first', 'month-first'])('speichert 30. Dezember bis 2. Januar (%s)', async (order) => {
    mockApi({ id: 1, name: 'Testsender', number: 1, color: '', enabled: true, kind: 'seasonal', config: {
      seasons: [{ id: 'silvester', name: 'Silvester', from: '12-01', to: '12-31', source: { type: 'rules', rules: { match: 'all', rules: [], groups: [] } } }],
    } })
    const { container } = render(ChannelEditor, { id: '1' })
    await fireEvent.click(await screen.findByRole('button', { name: /Silvester.*1\. Dez/ }))
    const [fromDay, fromMonth, toDay, toMonth] = container.querySelectorAll('.season-body .meta select')
    expect(fromMonth.value).toBe('12')
    expect(toDay.value).toBe('31')
    expect(toMonth.value).toBe('12')
    await fireEvent.change(fromDay, { target: { value: '30' } })
    const changes = order === 'day-first' ? [[toDay, '2'], [toMonth, '1']] : [[toMonth, '1'], [toDay, '2']]
    for (const [select, value] of changes) await fireEvent.change(select, { target: { value } })
    await fireEvent.click(screen.getAllByRole('button', { name: 'Speichern', exact: true })[0])
    await waitFor(() => {
      const request = fetch.mock.calls.find(([url, options]) => url === '/api/channels/1' && options?.method === 'PUT')
      expect(request).toBeTruthy()
      expect(JSON.parse(request[1].body).config.seasons[0]).toMatchObject({ from: '12-30', to: '01-02' })
    })
    expect(errors).toEqual([])
  })
})

describe('Quellen, die im Editor fast leer entstehen', () => {
  for (const type of ['manual', 'rules', 'ai', 'trailers']) {
    it(type, async () => {
      mockApi({})
      render(SourceEditor, { source: { type }, channel: { kind: 'smart', config: { source: { type: 'rules' } } } })
      await new Promise((r) => setTimeout(r, 50))
      expect(errors).toEqual([])
    })
  }
})

describe('normalizeConfig', () => {
  it('ergänzt alle gebundenen Felder', () => {
    const cfg = normalizeConfig({ filler: { type: 'trailers' }, source: { type: 'rules', rules: { rules: [{ field: 'genre', op: 'is' }] } } }, 'smart')
    for (const k of ['order', 'transcode', 'noRepeatHours', 'keepEpisodeOrder', 'logoNumber', 'logoHoliday', 'align', 'fillerBetween', 'previews']) {
      expect(cfg[k], k).not.toBeUndefined()
    }
    expect(cfg.watermark).toEqual({ enabled: false, position: 'tr', opacity: 70, size: 14, tone: 'color' })
    expect(cfg.source.rules).toEqual({ match: 'all', rules: [{ field: 'genre', op: 'is', values: [] }], groups: [] })
    expect(cfg.filler.channelFilter).toBe(false)
    expect(cfg.filler.trailerOrigin).toBe('library')
    const seasonal = normalizeConfig({ seasons: [{ id: 'x', source: { type: 'manual' } }] }, 'seasonal')
    expect(seasonal.seasons[0].source.items).toEqual([])
    expect(seasonal.fallback.rules.rules).toEqual([])
  })
})

describe('Planungsanzeige', () => {
  it('zeigt die KI-Planung und lädt danach „Als Nächstes“ neu', async () => {
    let planning = true
    let guideCalls = 0
    globalThis.fetch = vi.fn(async (url) => {
      const u = String(url)
      let body = {}
      if (u === '/api/channels/1/error') body = { error: '', planning }
      else if (u.startsWith('/api/channels/')) body = { id: 1, name: 'Heist', number: 1, color: '', enabled: true, kind: 'ai', planning: true, autoLogo: true, logoUrl: '', missing: [],
        config: { source: { type: 'ai', ai: { theme: 'Heist' } } } }
      else if (u.startsWith('/api/guide')) {
        guideCalls++
        body = planning ? [] : [{ channel: { id: 1 }, programs: [{ start: Date.now(), stop: Date.now() + 1, media: { title: "Ocean's Eleven" } }] }]
      } else if (u.startsWith('/api/preview')) body = { count: 0, movies: 0, episodes: 0, shows: 0, trailers: 0, durationMs: 0, items: [] }
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    render(ChannelEditor, { id: '1' })
    expect((await screen.findByRole('status')).textContent).toContain('KI plant gerade')
    expect(screen.getByRole('button', { name: /Neu planen/ }).disabled).toBe(true)
    const before = guideCalls
    planning = false
    expect(await screen.findByText("Ocean's Eleven", {}, { timeout: 5000 })).toBeTruthy()
    expect(guideCalls).toBeGreaterThan(before)
    expect(screen.queryByRole('status')).toBeNull()
    expect(screen.getByRole('button', { name: /Neu planen/ }).disabled).toBe(false)
    expect(errors).toEqual([])
  }, 10000)

  it('markiert planende Sender in der Senderliste, bis sie fertig sind', async () => {
    let planning = true
    globalThis.fetch = vi.fn(async (url) => {
      const u = String(url)
      const body = u === '/api/channels' ? [{ id: 1, name: 'Heist', number: 23, enabled: true, kind: 'ai', logoUrl: '', planning }] : []
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    render(Channels)
    expect((await screen.findByRole('status')).textContent).toContain('Plant')
    planning = false
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull(), { timeout: 5000 })
    expect(errors).toEqual([])
  }, 10000)
})
