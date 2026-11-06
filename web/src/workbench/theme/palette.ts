/*
 * 配色方案：预设种子、自定义方案推导与应用。
 *
 * 推导规则逐条对应 web/design-system/DESIGN.md「自定义方案」，改这里前先改规范。
 * 预设方案的 token 由 tokens.css 的 [data-palette] 规则声明；这里的预设种子只用于
 * 设置页色板展示，以及「从当前预设复制」作为自定义起点。
 */

export type ThemeMode = 'dark' | 'light'
export type PresetPalette = 'neutral' | 'mist' | 'graphite' | 'cobalt' | 'sage' | 'amber'
export type PaletteKey = PresetPalette | 'custom'

export interface PaletteSeeds {
  bg: string
  fg: string
  primary: string
  border: string
  sidebar: string
}

export type CustomPalette = Record<ThemeMode, PaletteSeeds>

export const SEED_KEYS: (keyof PaletteSeeds)[] = ['bg', 'fg', 'primary', 'border', 'sidebar']

export const PRESET_PALETTES: Record<PresetPalette, CustomPalette> = {
  neutral: {
    light: { bg: '#FFFFFF', fg: '#0A0A0A', primary: '#171717', border: '#E5E5E5', sidebar: '#FAFAFA' },
    dark: { bg: '#131416', fg: '#D7D7DB', primary: '#D0D0D6', border: '#2D2D30', sidebar: '#19191C' },
  },
  mist: {
    light: { bg: '#FFFFFF', fg: '#282C34', primary: '#2E5FA6', border: '#D0D7E0', sidebar: '#EFF2F6' },
    dark: { bg: '#212429', fg: '#CACED6', primary: '#9DB1CD', border: '#393E46', sidebar: '#22252A' },
  },
  graphite: {
    light: { bg: '#FFFFFF', fg: '#2A2D33', primary: '#48505D', border: '#D2D7E0', sidebar: '#EBEEF3' },
    dark: { bg: '#202227', fg: '#C6C9CF', primary: '#AEB5C0', border: '#383C43', sidebar: '#212328' },
  },
  cobalt: {
    light: { bg: '#FFFFFF', fg: '#202B3A', primary: '#2B63B7', border: '#C8D4E5', sidebar: '#E8EFF8' },
    dark: { bg: '#1B222E', fg: '#C6CFDB', primary: '#89ADDF', border: '#343F4F', sidebar: '#1C2330' },
  },
  sage: {
    light: { bg: '#FFFFFF', fg: '#24302B', primary: '#3A7B6A', border: '#CBD9D3', sidebar: '#EBF2EE' },
    dark: { bg: '#1D2623', fg: '#C6D0CC', primary: '#8AB8A9', border: '#35433E', sidebar: '#1E2724' },
  },
  amber: {
    light: { bg: '#FFFFFA', fg: '#383020', primary: '#955F1C', border: '#E0CFA8', sidebar: '#F7EED5' },
    dark: { bg: '#27241C', fg: '#D9D0BC', primary: '#D5A94B', border: '#473F2E', sidebar: '#29251D' },
  },
}

export const PRESET_KEYS = Object.keys(PRESET_PALETTES) as PresetPalette[]

export const DEFAULT_CUSTOM_PALETTE: CustomPalette = {
  light: { ...PRESET_PALETTES.neutral.light },
  dark: { ...PRESET_PALETTES.neutral.dark },
}

/* ───────────────────────── 颜色运算 ───────────────────────── */

type RGB = [number, number, number]

const WHITE: RGB = [255, 255, 255]
const BLACK: RGB = [0, 0, 0]

export function hexToRgb(hex: string): RGB | null {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) return null
  const h = m[1].length === 3 ? m[1].replace(/(.)/g, '$1$1') : m[1]
  const n = parseInt(h, 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}

export function rgbToHex(c: RGB): string {
  return '#' + c.map((v) => v.toString(16).padStart(2, '0')).join('').toUpperCase()
}

const channels = (c: RGB) => c.join(' ')

const mix = (a: RGB, b: RGB, w: number): RGB => [
  Math.round(a[0] + (b[0] - a[0]) * w),
  Math.round(a[1] + (b[1] - a[1]) * w),
  Math.round(a[2] + (b[2] - a[2]) * w),
]

const lin = (v: number) => {
  const s = v / 255
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
}
const luminance = (c: RGB) => 0.2126 * lin(c[0]) + 0.7152 * lin(c[1]) + 0.0722 * lin(c[2])
const contrast = (a: RGB, b: RGB) => {
  const x = luminance(a)
  const y = luminance(b)
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
}
const minContrast = (c: RGB, grounds: RGB[]) => Math.min(...grounds.map((g) => contrast(c, g)))

/** 从 from 向 to 以 0.01 步长推进，返回第一个满足 ok 的颜色；推进到 to 仍不满足时返回 null。 */
function pushUntil(from: RGB, to: RGB, ok: (c: RGB) => boolean): RGB | null {
  for (let i = 0; i <= 100; i++) {
    const c = mix(from, to, i / 100)
    if (ok(c)) return c
  }
  return null
}

const higherContrast = (c: RGB): RGB => (contrast(c, WHITE) >= contrast(c, BLACK) ? WHITE : BLACK)

/* ───────────────────────── 推导 ───────────────────────── */

interface Weights {
  base: number
  popover: number
  elevated: number
  hover: number
  ring: number
  selHover: number
  tint: number
}

const WEIGHTS: Record<ThemeMode, Weights> = {
  light: { base: 0.04, popover: 0, elevated: 0.06, hover: 0.06, ring: 0.62, selHover: 0.12, tint: 0.12 },
  dark: { base: 0.04, popover: 0.06, elevated: 0.12, hover: 0.14, ring: 0.42, selHover: 0.1, tint: 0.16 },
}

/* ── 列表选中：surface 混入方案主色色相的低饱和染底，靠色相而不是明度与周围拉开 ── */

// 混合比例按「与 surface 的对比度」求解，而不是固定权重：黄、绿色相天生更亮，固定比例会让暖色方案的选中底几乎看不见
const SELECTION_CONTRAST: Record<ThemeMode, { focused: number; selected: number }> = {
  light: { focused: 1.22, selected: 1.1 },
  dark: { focused: 1.4, selected: 1.22 },
}
// 主色饱和度低于此值（中性 / 石墨这类灰阶主色）时没有可用色相，退回蓝色
const MIN_SELECTION_SATURATION = 0.15
const FALLBACK_SELECTION_HUE = 217

function hue(c: RGB): { h: number; s: number } {
  const [r, g, b] = c.map((v) => v / 255)
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const l = (max + min) / 2
  const d = max - min
  if (d === 0) return { h: 0, s: 0 }
  const s = d / (1 - Math.abs(2 * l - 1))
  const h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4
  return { h: (h * 60 + 360) % 360, s }
}

function hslToRgb(h: number, s: number, l: number): RGB {
  const c = (1 - Math.abs(2 * l - 1)) * s
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1))
  const m = l - c / 2
  const [r, g, b] = h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x]
  return [Math.round((r + m) * 255), Math.round((g + m) * 255), Math.round((b + m) * 255)]
}

export interface SelectionColors {
  selected: RGB
  focused: RGB
  indicator: RGB
}

/**
 * 选中行染底与当前行竖条。checks 是必须在两种选中底上保持的文字对比度，
 * 不满足时逐档降低混合比例；预设方案传入其文字与语义色，自定义方案的文字色随后自行推导达标，只传 fg。
 */
export interface SelectionCheck {
  color: RGB
  /** 在焦点行 / 多选行底色上的对比度下限 */
  focused: number
  selected: number
}

export function deriveSelection(surface: RGB, primary: RGB, mode: ThemeMode, checks: SelectionCheck[]): SelectionColors {
  const { h, s } = hue(primary)
  const tint = hslToRgb(s < MIN_SELECTION_SATURATION ? FALLBACK_SELECTION_HUE : h, 0.8, mode === 'light' ? 0.45 : 0.68)
  const passes = (c: RGB, which: 'focused' | 'selected') => checks.every((k) => contrast(k.color, c) >= k[which])
  // 从目标对比度对应的比例起步；文字对比度不达标时逐档回退，宁可选中底淡一些也不牺牲可读性
  const solve = (which: 'focused' | 'selected') => {
    const target = SELECTION_CONTRAST[mode][which]
    let w = 0
    while (w < 0.6 && contrast(mix(surface, tint, w), surface) < target) w = Math.round((w + 0.01) * 100) / 100
    while (w > 0 && !passes(mix(surface, tint, w), which)) w = Math.round((w - 0.01) * 100) / 100
    return mix(surface, tint, w)
  }
  const focused = solve('focused')
  const selected = solve('selected')
  // 竖条是非文本元素，与选中底和内容底都要 ≥3:1
  const end = mode === 'light' ? BLACK : WHITE
  const indicator = pushUntil(tint, end, (c) => contrast(c, focused) >= 3 && contrast(c, surface) >= 3) ?? end
  return { selected, focused, indicator }
}

// 语义色 / method 色 / 类型色的预设默认值（中性方案），自定义方案在其上按对比度微调明度
const SEMANTIC_DEFAULTS: Record<ThemeMode, Record<string, string>> = {
  light: {
    ok: '#166534', warn: '#92400E', danger: '#B91C1C', info: '#1D4ED8', violet: '#115E59',
    'method-get': '#166534', 'method-post': '#1D4ED8', 'method-put': '#92400E', 'method-delete': '#B91C1C', 'method-patch': '#115E59',
    'type-number': '#1D4ED8', 'type-string': '#166534', 'type-boolean': '#9A3412', 'type-key': '#7E22CE', 'type-binary': '#B91C1C',
  },
  dark: {
    ok: '#4ADE80', warn: '#FBBF24', danger: '#F87474', info: '#60A5FA', violet: '#2DD4BF',
    'method-get': '#4ADE80', 'method-post': '#60A5FA', 'method-put': '#FBBF24', 'method-delete': '#F87474', 'method-patch': '#2DD4BF',
    'type-number': '#93C5FD', 'type-string': '#86EFAC', 'type-boolean': '#FDBA74', 'type-key': '#D8B4FE', 'type-binary': '#FCA5A5',
  },
}

const SOLID_FG = ['ok', 'warn', 'danger', 'info'] as const

/** 由 bg、fg 派生的底面（不含 inset，它来自 sidebar 种子）。 */
function fgGrounds(bg: RGB, fg: RGB, w: Weights, primary: RGB, mode: ThemeMode): RGB[] {
  const rows = deriveSelection(bg, primary, mode, [])
  return [bg, mix(bg, fg, w.base), mix(bg, fg, w.popover), mix(bg, fg, w.elevated), mix(bg, fg, w.hover), rows.selected, rows.focused]
}

export interface DerivedPalette {
  /** 写入 :root 内联样式的变量，值为空格分隔 RGB 通道 */
  vars: Record<string, string>
  /** 为保证可读被校正过的种子：键为种子名，值为实际生效色 */
  adjusted: Partial<PaletteSeeds>
}

export function derivePalette(seeds: PaletteSeeds, mode: ThemeMode): DerivedPalette {
  const w = WEIGHTS[mode]
  const end = mode === 'light' ? BLACK : WHITE
  const antiEnd = mode === 'light' ? WHITE : BLACK
  const polar = (fg: RGB, bg: RGB) => (mode === 'light' ? luminance(fg) < luminance(bg) : luminance(fg) > luminance(bg))
  const primary = hexToRgb(seeds.primary) ?? hexToRgb(DEFAULT_CUSTOM_PALETTE[mode].primary)!
  const readable = (fg: RGB, bg: RGB) => polar(fg, bg) && minContrast(fg, fgGrounds(bg, fg, w, primary, mode)) >= 7

  const seedBg = hexToRgb(seeds.bg) ?? hexToRgb(DEFAULT_CUSTOM_PALETTE[mode].bg)!
  const seedFg = hexToRgb(seeds.fg) ?? hexToRgb(DEFAULT_CUSTOM_PALETTE[mode].fg)!
  const line = hexToRgb(seeds.border) ?? hexToRgb(DEFAULT_CUSTOM_PALETTE[mode].border)!
  const seedSidebar = hexToRgb(seeds.sidebar) ?? hexToRgb(DEFAULT_CUSTOM_PALETTE[mode].sidebar)!

  // 1. 输入约束：先推 fg，fg 到端点仍不满足再推 bg；两者都到端点时必然满足
  let bg = seedBg
  let fg = pushUntil(seedFg, end, (c) => readable(c, bg))
  if (!fg) {
    fg = end
    bg = pushUntil(seedBg, antiEnd, (c) => readable(end, c)) ?? antiEnd
  }
  const sidebarOk = (c: RGB) => polar(fg!, c) && contrast(fg!, c) >= 7
  const inset = pushUntil(seedSidebar, bg, sidebarOk) ?? bg

  const adjusted: Partial<PaletteSeeds> = {}
  if (rgbToHex(bg) !== rgbToHex(seedBg)) adjusted.bg = rgbToHex(bg)
  if (rgbToHex(fg) !== rgbToHex(seedFg)) adjusted.fg = rgbToHex(fg)
  if (rgbToHex(inset) !== rgbToHex(seedSidebar)) adjusted.sidebar = rgbToHex(inset)

  // 2. 推导
  const surface = bg
  const base = mix(bg, fg, w.base)
  const popover = mix(bg, fg, w.popover)
  const elevated = mix(bg, fg, w.elevated)
  const hover = mix(bg, fg, w.hover)
  const rows = deriveSelection(bg, primary, mode, [])
  const rowSelected = rows.selected
  const rowFocused = rows.focused
  const grounds = [base, inset, surface, popover, elevated, hover, rowSelected, rowFocused]
  const chrome = [surface, base, inset]

  const fgMuted =
    pushUntil(mix(fg, bg, 0.4), fg, (c) => contrast(c, rowSelected) >= 6 && minContrast(c, grounds) >= 5) ?? fg
  let fgFaint = fgMuted
  for (let i = 1; i <= 100; i++) {
    const c = mix(fgMuted, inset, i / 100)
    if (minContrast(c, grounds) < 4.6) break
    fgFaint = c
  }

  const accent = pushUntil(primary, fg, (c) => minContrast(c, chrome) >= 4.6) ?? fg
  let accentHover = pushUntil(accent, end, (c) => contrast(c, accent) >= 1.2)
  if (!accentHover) {
    accentHover = accent
    for (let i = 20; i >= 0; i--) {
      const c = mix(accent, bg, i / 100)
      if (minContrast(c, chrome) >= 4.6) {
        accentHover = c
        break
      }
    }
  }

  const selFor = (sel: RGB) => {
    const selHover = mix(sel, bg, w.selHover)
    const score = (f: RGB) => Math.min(contrast(f, sel), contrast(f, selHover))
    const selFg = score(WHITE) >= score(BLACK) ? WHITE : BLACK
    return { sel, selHover, selFg, score: score(selFg) }
  }
  let selSet = selFor(primary)
  if (selSet.score < 4.5) {
    const away = selSet.selFg === WHITE ? BLACK : WHITE
    for (let i = 1; i <= 100 && selSet.score < 4.5; i++) selSet = selFor(mix(primary, away, i / 100))
  }

  const ring = mix(fg, bg, w.ring)

  const vars: Record<string, string> = {
    '--c-surface': channels(surface),
    '--c-base': channels(base),
    '--c-inset': channels(inset),
    '--c-popover': channels(popover),
    '--c-elevated': channels(elevated),
    '--c-hover': channels(hover),
    '--c-row-selected': channels(rowSelected),
    '--c-row-focused': channels(rowFocused),
    '--c-row-indicator': channels(rows.indicator),
    '--c-line': channels(line),
    '--c-line-strong': channels(mix(line, fg, 0.08)),
    '--c-ring': channels(ring),
    '--c-fg': channels(fg),
    '--c-fg-muted': channels(fgMuted),
    '--c-fg-faint': channels(fgFaint),
    '--c-accent': channels(accent),
    '--c-accent-hover': channels(accentHover),
    '--c-accent-fg': channels(higherContrast(accent)),
    '--c-sel': channels(selSet.sel),
    '--c-sel-hover': channels(selSet.selHover),
    '--c-sel-fg': channels(selSet.selFg),
    '--c-type-null': channels(fgFaint),
    '--c-method-other': channels(fgMuted),
  }

  const semanticGrounds = [surface, base, inset, hover, rowSelected, rowFocused]
  for (const [name, hex] of Object.entries(SEMANTIC_DEFAULTS[mode])) {
    const ok = (c: RGB) => minContrast(c, [...semanticGrounds, mix(surface, c, w.tint)]) >= 4.5
    vars[`--c-${name}`] = channels(pushUntil(hexToRgb(hex)!, end, ok) ?? end)
  }
  for (const name of SOLID_FG) {
    const [r, g, b] = vars[`--c-${name}`].split(' ').map(Number)
    vars[`--c-${name}-fg`] = channels(higherContrast([r, g, b]))
  }

  return { vars, adjusted }
}

/* ───────────────────────── 应用 ───────────────────────── */

// 自定义方案可能写过的全部内联变量，外加旧强调色实现遗留的内联变量；切换方案前一律清掉，
// 否则内联值会继续压过 tokens.css 的预设规则。--wb-font-size 也走内联，不在此列。
const INLINE_VARS = [
  ...Object.keys(derivePalette(DEFAULT_CUSTOM_PALETTE.light, 'light').vars),
  ...SOLID_FG.map((n) => `--c-${n}-fg`),
  '--wb-selection',
]

export function applyPalette(palette: PaletteKey, custom: CustomPalette, mode: ThemeMode) {
  const root = document.documentElement
  for (const v of new Set(INLINE_VARS)) root.style.removeProperty(v)
  root.setAttribute('data-palette', palette)
  if (palette !== 'custom') return
  const { vars } = derivePalette(custom[mode] ?? DEFAULT_CUSTOM_PALETTE[mode], mode)
  for (const [k, v] of Object.entries(vars)) root.style.setProperty(k, v)
}
