// Dates, times and numbers are formatted in the language of the interface
// (locale() of lib/i18n.svelte.js), never in the browser's or the system's:
// otherwise the page mixes languages for users with another browser language,
// and tests pass or fail depending on the computer they run on.
import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'

const root = resolve(process.cwd(), 'src')
// a formatting call without a language: toLocaleDateString(), new Intl.NumberFormat() …
const bad = [
  /\.toLocale(?:Date|Time)?String\(\s*\)/,
  /\.toLocale(?:Date|Time)?String\(\s*(?:undefined|null|\[\])\s*[,)]/,
  /\bIntl\.[A-Za-z]+\(\s*\)/,
  /\bIntl\.[A-Za-z]+\(\s*(?:undefined|null|\[\])\s*[,)]/,
]

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return name === 'tests' ? [] : files(path)
    return /\.(svelte|js)$/.test(name) ? [path] : []
  })
}

export function offenders(sources) {
  const out = []
  for (const [file, text] of sources) {
    text.split('\n').forEach((line, i) => {
      if (bad.some((re) => re.test(line))) out.push(`${file}:${i + 1}: ${line.trim()}`)
    })
  }
  return out
}

describe('formatting language', () => {
  it('every date, time and number names its language', () => {
    const sources = files(root).map((f) => [relative(process.cwd(), f), readFileSync(f, 'utf8')])
    expect(offenders(sources)).toEqual([])
  })
  it('finds calls without a language', () => {
    const found = offenders([['x.svelte', [
      'new Date(ms).toLocaleDateString()',
      'n.toLocaleString(undefined, { maximumFractionDigits: 1 })',
      'new Intl.NumberFormat().format(n)',
      'new Date(ms).toLocaleTimeString(locale(), opts)',
      "new Intl.DateTimeFormat(locale(), { weekday: 'short' })",
    ].join('\n')]])
    expect(found.map((l) => l.split(':')[1])).toEqual(['1', '2', '3'])
  })
})
