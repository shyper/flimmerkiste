// The small shared helpers every page leans on: the API wrapper, the router's
// leave guard and the poller.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { api, ApiError } from '../lib/api.js'
import { app } from '../lib/state.svelte.js'
import { navigate, leaveGuard, route } from '../lib/router.svelte.js'
import { poll } from '../lib/poll.js'
import { setLang } from '../lib/i18n.svelte.js'

const answer = (status, body, type = 'application/json') =>
  (globalThis.fetch = vi.fn(async () => new Response(body, { status, headers: { 'Content-Type': type } })))

afterEach(() => vi.restoreAllMocks())

describe('api', () => {
  it('returns the JSON answer', async () => {
    answer(200, '{"ok":true}')
    expect(await api('GET', '/api/x')).toEqual({ ok: true })
  })
  it('throws the server message with the status', async () => {
    answer(400, '{"error":"Bitte einen Namen angeben"}')
    const err = await api('PUT', '/api/channels/1', {}).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).toBe('Bitte einen Namen angeben')
    expect(err.status).toBe(400)
  })
  it('names the status when the answer is no JSON', async () => {
    setLang('en')
    answer(502, 'Bad Gateway', 'text/plain')
    const err = await api('GET', '/api/x').catch((e) => e)
    expect(err.status).toBe(502)
    expect(err.message).toContain('502')
    setLang('de')
  })
  it('signs out on 401, except for the sign-in calls', async () => {
    app.user = { id: 1 }
    answer(401, '{"error":"x"}')
    await api('GET', '/api/auth/status').catch(() => {})
    expect(app.user).toEqual({ id: 1 })
    await api('GET', '/api/channels').catch(() => {})
    expect(app.user).toBeNull()
  })
})

describe('leaveGuard', () => {
  it('asks every page that holds a check, and only the holder releases it', () => {
    const stay = () => false
    const other = () => true
    leaveGuard.hold(stay)
    leaveGuard.hold(other)
    navigate('/guide')
    expect(route.path).not.toBe('/guide')
    leaveGuard.release(other) // the other page goes: the first one still guards
    navigate('/guide')
    expect(route.path).not.toBe('/guide')
    leaveGuard.release(stay)
    navigate('/guide')
    expect(route.path).toBe('/guide')
    navigate('/')
  })
})

describe('poll', () => {
  it('runs nothing after stop, also when stopped during a round', async () => {
    vi.useFakeTimers()
    let calls = 0
    let finish
    const p = poll(() => { calls++; return new Promise((r) => (finish = r)) }, 1000)
    expect(calls).toBe(1)
    p.stop()
    finish()
    await vi.advanceTimersByTimeAsync(5000)
    expect(calls).toBe(1)
    vi.useRealTimers()
  })
  it('asks the delay every round and survives errors', async () => {
    vi.useFakeTimers()
    let calls = 0
    const p = poll(() => { calls++; throw new Error('down') }, () => (calls < 2 ? 100 : 1000))
    await vi.advanceTimersByTimeAsync(150)
    expect(calls).toBe(2)
    await vi.advanceTimersByTimeAsync(500)
    expect(calls).toBe(2)
    await vi.advanceTimersByTimeAsync(600)
    expect(calls).toBe(3)
    p.stop()
    vi.useRealTimers()
  })
})
