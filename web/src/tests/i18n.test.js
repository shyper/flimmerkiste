// Both languages have the same texts with the same placeholders, and the
// markup has no visible text that bypasses t().
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { parse } from 'svelte/compiler'
import de from '../locales/de.json'
import en from '../locales/en.json'

const placeholders = (v) => [...new Set((typeof v === 'string' ? v : Object.values(v).join(' ')).match(/\{\w+\}/g) || [])].sort()

describe('catalogs', () => {
  it('have the same keys', () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(de).sort())
  })
  it('have the same placeholders', () => {
    for (const k of Object.keys(de)) expect([k, placeholders(en[k])]).toEqual([k, placeholders(de[k])])
  })
  it('have no empty texts', () => {
    for (const [k, v] of [...Object.entries(de), ...Object.entries(en)]) expect([k, v]).not.toEqual([k, ''])
  })
})

// Visible text that may stay as it is: code, addresses, brand names.
const allowed = /^([\w.:/\-?=&%#@,…·+→↖↗↙↘×•()\s]*[/.:=#@?&%][\w.:/\-?=&%#@,…\s]*|Plex|fk-|Claude \(Anthropic\)|1080p|VHS|sk-ant-…|●|★)$/
const textAttrs = new Set(['title', 'placeholder', 'aria-label', 'alt', 'label', 'hint', 'text'])

function visibleTexts(file) {
  const src = readFileSync(file, 'utf8')
  const found = []
  const walk = (n) => {
    if (!n || typeof n !== 'object') return
    if (Array.isArray(n)) return n.forEach(walk)
    if (n.type === 'Text' && /\p{L}{2,}/u.test(n.data) && !allowed.test(n.data.trim())) found.push(n.data.trim())
    if (n.type === 'Attribute') {
      if (textAttrs.has(n.name) && Array.isArray(n.value)) {
        for (const v of n.value) if (v.type === 'Text' && /\p{L}{2,}/u.test(v.data) && !allowed.test(v.data.trim())) found.push(`${n.name}="${v.data}"`)
      }
      return
    }
    for (const [k, v] of Object.entries(n)) if (k !== 'parent' && v && typeof v === 'object') walk(v)
  }
  walk(parse(src, { modern: true }).fragment)
  return found
}

describe('markup', () => {
  const dirs = ['src', 'src/pages', 'src/pages/settings', 'src/components']
  for (const dir of dirs) {
    for (const f of readdirSync(resolve(process.cwd(), dir)).filter((f) => f.endsWith('.svelte'))) {
      it(`${dir}/${f} has no text outside t()`, () => {
        expect(visibleTexts(resolve(process.cwd(), dir, f))).toEqual([])
      })
    }
  }
})

describe('catalog keys', () => {
  // Keys put together at run time: their families count as used.
  const dynamic = [/^settings\.group\./, /^settings\.\w+\.(title|intro)$/, /^ruleGroup\.(op|match)\./, /^sourceEditor\.idea\d$/, /^logo\.holiday\./]
  it('are all used', () => {
    const files = []
    const collect = (dir) => {
      for (const e of readdirSync(resolve(process.cwd(), dir), { withFileTypes: true })) {
        if (e.isDirectory() && e.name !== 'tests' && e.name !== 'locales') collect(`${dir}/${e.name}`)
        else if (/\.(svelte|js)$/.test(e.name)) files.push(readFileSync(resolve(process.cwd(), dir, e.name), 'utf8'))
      }
    }
    collect('src')
    const used = new Set(files.join('\n').match(/t\('[\w.]+'/g).map((m) => m.slice(3, -1)))
    expect(Object.keys(de).filter((k) => !used.has(k) && !dynamic.some((r) => r.test(k)))).toEqual([])
  })
})
