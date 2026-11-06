import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { derivePalette, deriveSelection, PRESET_PALETTES, type ThemeMode } from './palette'

const tokens = readFileSync(resolve(__dirname, 'tokens.css'), 'utf8')

type RGB = [number, number, number]

function block(selector: string): Record<string, RGB> {
  const start = tokens.indexOf(selector)
  const body = tokens.slice(start, tokens.indexOf('}', start))
  const out: Record<string, RGB> = {}
  for (const m of body.matchAll(/(--c-[\w-]+):\s*(\d+) (\d+) (\d+);/g)) out[m[1]] = [+m[2], +m[3], +m[4]]
  return out
}

const DEFAULT_BLOCK: Record<ThemeMode, string> = {
  light: ":root,\n:root[data-theme='light']",
  dark: ":root[data-theme='dark'] {",
}

describe('derivePalette', () => {
  it.each<ThemeMode>(['light', 'dark'])('中性方案种子推导结果与默认 token 一致（%s，误差 ±8）', (mode) => {
    const expected = block(DEFAULT_BLOCK[mode])
    const { vars, adjusted } = derivePalette(PRESET_PALETTES.neutral[mode], mode)
    expect(adjusted).toEqual({})
    for (const [name, value] of Object.entries(vars)) {
      // 实底前景按规范取纯黑/纯白，深色预设则用深底色 #131416，两者本就不同
      if (!(name in expected) || name.endsWith('-fg')) continue
      const got = value.split(' ').map(Number)
      // 次要/弱文字推导取刚好达标的灰度，预设值留有余量；method-other 推导时即取 fg-muted
      const tolerance = ['--c-fg-muted', '--c-fg-faint', '--c-method-other'].includes(name) ? 12 : 8
      got.forEach((v, i) => expect(Math.abs(v - expected[name][i]), `${name}`).toBeLessThanOrEqual(tolerance))
    }
  })

  it('不可读的种子被校正，并报告实际生效色', () => {
    const { vars, adjusted } = derivePalette(
      { bg: '#808080', fg: '#808080', primary: '#808080', border: '#808080', sidebar: '#808080' },
      'light',
    )
    expect(adjusted.fg).toBe('#000000')
    expect(adjusted.bg).toBeDefined()
    expect(vars['--c-fg']).toBe('0 0 0')
  })

  const SEMANTIC = ['ok', 'warn', 'danger', 'info', 'violet', 'method-get', 'method-post', 'method-put', 'method-delete', 'method-patch', 'type-number', 'type-string', 'type-boolean', 'type-key', 'type-binary']

  it.each(Object.keys(PRESET_PALETTES).flatMap((p) => (['light', 'dark'] as const).map((m) => [p, m] as const)))(
    'tokens.css 中 %s / %s 的选中色与 deriveSelection 生成结果一致',
    (palette, mode) => {
      const override =
        palette === 'neutral'
          ? {}
          : block(mode === 'light' ? `:root[data-palette='${palette}']:not([data-theme='dark']) {` : `:root[data-palette='${palette}'][data-theme='dark'] {`)
      const t = { ...block(DEFAULT_BLOCK[mode]), ...override }
      const checks = [
        { color: t['--c-fg'], focused: 6.5, selected: 6.5 },
        { color: t['--c-fg-muted'], focused: 5, selected: 6 },
        { color: t['--c-fg-faint'], focused: 4.6, selected: 4.6 },
        ...SEMANTIC.map((k) => ({ color: t[`--c-${k}`], focused: 4.5, selected: 4.5 })),
      ]
      const r = deriveSelection(t['--c-surface'], t['--c-sel'], mode, checks)
      expect([t['--c-row-selected'], t['--c-row-focused'], t['--c-row-indicator']]).toEqual([r.selected, r.focused, r.indicator])
    },
  )
})
