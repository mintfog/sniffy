import { useEffect } from 'react'
import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'
import { Events } from '@wailsio/runtime'
import { Bridge } from '@/lib/bridge'

/*
 * 工作台统一偏好（单一真相源 + 持久化 + 跨窗口同步）。
 *
 * 这里收口了原先散落各处的 UI 偏好：主题、强调色、紧凑/字号、搜索框可见性、
 * 跟随最新、Body 查看模式、详情面板宽度/分栏、以及代理相关开关。全部写入
 * localStorage（key: sniffy-prefs），刷新/重启后保留。
 *
 * 桌面端是多窗口的（设置/工具箱/关于会弹出独立系统窗）。usePrefsBridge() 会：
 *   1. 把「全局外观类」偏好实时写进 :root 的 CSS 变量（主题/强调色/密度/字号）；
 *   2. 经 Wails 事件总线把全局偏好广播到其它窗口，使各窗口外观保持一致。
 */

export type ThemeMode = 'dark' | 'light'
export type PresetAccent = 'sky' | 'prussian' | 'teal' | 'vermilion' | 'brass' | 'slate'
export type AccentKey = PresetAccent | 'custom'
export type BodyMode = 'tree' | 'raw' | 'hex'
export type DecryptScope = 'all' | 'allow' | 'deny'
export type FontSize = 12 | 13 | 14

export interface Prefs {
  // —— 全局外观（跨窗口同步） ——
  theme: ThemeMode
  accent: AccentKey
  /** accent==='custom' 时生效的任意主色（#rrggbb） */
  accentCustom: string
  compact: boolean
  fontSize: FontSize

  // —— 流量视图 UI（主窗口本地） ——
  searchVisible: boolean
  follow: boolean
  bodyMode: BodyMode
  /** 详情面板宽度（px）。0 = 未自定义，运行时按窗口宽度的一半计算。 */
  detailWidth: number
  /** 详情面板内「请求区」占比（0–1）。 */
  detailTopFrac: number
  /** 用户选定的本机内网地址（多网卡时，用于代理监听地址展示）。空 = 跟随后端推荐项。 */
  lanIP: string

  // —— 代理 / 抓包（前端持有，后端接线后再下发） ——
  systemProxy: boolean
  /** 每次启动是否自动开启系统代理（systemProxy 为运行时当前开关）。 */
  autoSystemProxy: boolean
  throttle: boolean
  port: string
  mitm: boolean
  scope: DecryptScope
  upstream: boolean
  upstreamAddr: string
  maxFlows: number
  autoRecord: boolean

  // —— 应用行为 ——
  /** 关闭主窗口时的行为:true 隐藏到托盘继续后台运行,点托盘图标可再打开;false 直接退出。 */
  runInBackground: boolean
}

interface PrefsStore extends Prefs {
  /** 合并补丁（本地修改入口；usePrefsBridge 会据此广播全局键） */
  set: (patch: Partial<Prefs>) => void
  /** 远端补丁（来自其它窗口，不再广播，避免回声） */
  merge: (patch: Partial<Prefs>) => void
  reset: () => void
}

const DEFAULTS: Prefs = {
  theme: 'dark',
  accent: 'sky',
  accentCustom: '#4A90C0',
  compact: false,
  fontSize: 13,
  searchVisible: false,
  follow: true,
  bodyMode: 'tree',
  detailWidth: 0,
  detailTopFrac: 0.45,
  lanIP: '',
  systemProxy: true,
  autoSystemProxy: true,
  throttle: false,
  port: '8080',
  mitm: true,
  scope: 'all',
  upstream: false,
  upstreamAddr: '',
  maxFlows: 10000,
  autoRecord: true,
  runInBackground: true,
}

// 是否为独立子窗口（?w=settings|tools|about）。
const isStandalone =
  typeof window !== 'undefined' && !!new URLSearchParams(window.location.search).get('w')

// 「仅主窗口拥有」的 UI 键：不跨窗口同步、也只有主窗口会编辑。
// 独立子窗口持久化时必须保留 localStorage 中这些键的现值，
// 否则子窗口的整快照写会用自己的陈旧默认值覆盖主窗口刚写入的值（last-writer-wins）。
const UI_ONLY_KEYS: (keyof Prefs)[] = ['searchVisible', 'bodyMode', 'detailWidth', 'detailTopFrac', 'lanIP']

// 自定义字符串存储：独立子窗口写入时跳过 UI_ONLY_KEYS（保留既有值），杜绝跨窗覆盖。
const prefsStringStorage = {
  getItem: (name: string) => (typeof window === 'undefined' ? null : window.localStorage.getItem(name)),
  removeItem: (name: string) => {
    if (typeof window !== 'undefined') window.localStorage.removeItem(name)
  },
  setItem: (name: string, value: string) => {
    if (typeof window === 'undefined') return
    if (isStandalone) {
      try {
        const incoming = JSON.parse(value) as { state?: Record<string, unknown> }
        if (incoming.state) {
          const prevRaw = window.localStorage.getItem(name)
          const prev = prevRaw ? (JSON.parse(prevRaw) as { state?: Record<string, unknown> }) : null
          for (const k of UI_ONLY_KEYS) {
            if (prev?.state && k in prev.state) incoming.state[k] = prev.state[k]
            else delete incoming.state[k]
          }
          window.localStorage.setItem(name, JSON.stringify(incoming))
          return
        }
      } catch {
        /* 解析失败则按原样写 */
      }
    }
    window.localStorage.setItem(name, value)
  },
}

export const usePrefs = create<PrefsStore>()(
  persist(
    (set) => ({
      ...DEFAULTS,
      set: (patch) => set(patch),
      merge: (patch) => set(patch),
      reset: () => set(DEFAULTS),
    }),
    {
      name: 'sniffy-prefs',
      version: 3,
      storage: createJSONStorage(() => prefsStringStorage),
      // 只持久化数据字段（动作不入库）
      partialize: (s) => {
        const { set: _s, merge: _m, reset: _r, ...data } = s
        return data
      },
      // v3:仅剔除已下架的 accent/theme 值,合法项照旧保留。
      migrate: (persisted, version) => {
        const p = persisted as Partial<Prefs> | undefined
        if (!p || typeof p !== 'object') return p
        if (version < 3) {
          const validAccents: AccentKey[] = ['sky', 'prussian', 'teal', 'vermilion', 'brass', 'slate', 'custom']
          if (!p.accent || !validAccents.includes(p.accent)) p.accent = 'sky'
          if (p.theme !== 'dark' && p.theme !== 'light') p.theme = 'dark'
          if (!p.accentCustom || p.accentCustom === '#1C6FB5') p.accentCustom = '#4A90C0'
        }
        return p
      },
    },
  ),
)

/* ───────────────────────── 强调色定义 ───────────────────────── */

interface AccentDef {
  /** 夜间蓝图(深色)下的 base / hover——更亮以在墨蓝底上拉开对比（空格分隔 RGB 通道） */
  dark: { base: string; hover: string }
  /** 亮色蓝图下需更深以在象牙纸上保证对比 */
  light: { base: string; hover: string }
  /** 强调底上的前景色,按主题分:亮色强调偏深→配纸白;夜间强调偏亮→配墨色 */
  fg: { dark: string; light: string }
  /** 设置面板色板展示色 */
  swatch: string
}

/* 强调色:默认天蓝(sky),另备普鲁士蓝、青、朱红、黄铜、石板几枚墨调;'custom' 走用户自选 hex。 */
export const ACCENTS: Record<PresetAccent, AccentDef> = {
  sky: {
    // 天蓝强调:亮色取较深(象牙纸上保证对比),深色取较亮。
    light: { base: '44 119 174', hover: '36 95 142' },
    dark: { base: '74 144 192', hover: '92 160 206' },
    fg: { light: '251 248 241', dark: '15 18 21' },
    swatch: '#4A90C0',
  },
  prussian: {
    light: { base: '28 111 181', hover: '21 90 150' },
    dark: { base: '88 172 230', hover: '112 188 238' },
    fg: { light: '251 248 241', dark: '14 24 34' },
    swatch: '#1C6FB5',
  },
  teal: {
    light: { base: '30 125 116', hover: '24 101 94' },
    dark: { base: '63 179 166', hover: '90 196 184' },
    fg: { light: '251 248 241', dark: '8 22 20' },
    swatch: '#1E7D74',
  },
  vermilion: {
    light: { base: '192 74 51', hover: '162 60 40' },
    dark: { base: '224 113 92', hover: '232 134 116' },
    fg: { light: '251 248 241', dark: '26 16 12' },
    swatch: '#C04A33',
  },
  brass: {
    /* base 取 4.5:1 以上:154 107 26 在纸白前景下仅 4.41,不达 AA */
    light: { base: '143 99 24', hover: '126 87 18' },
    dark: { base: '216 162 62', hover: '226 178 86' },
    fg: { light: '251 248 241', dark: '26 20 10' },
    swatch: '#8F6318',
  },
  slate: {
    light: { base: '62 76 90', hover: '46 58 70' },
    dark: { base: '133 149 164', hover: '154 169 183' },
    fg: { light: '251 248 241', dark: '14 22 30' },
    swatch: '#3E4C5A',
  },
}

/* ───────────────────────── CSS 变量应用 ───────────────────────── */

function applyTheme(theme: ThemeMode) {
  document.documentElement.setAttribute('data-theme', theme)
}

/** #rgb / #rrggbb → {r,g,b};非法返回 null */
function hexToRgb(hex: string): { r: number; g: number; b: number } | null {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) return null
  const h = m[1].length === 3 ? m[1].replace(/(.)/g, '$1$1') : m[1]
  const n = parseInt(h, 16)
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 }
}

/** 朝白(amt>0)或黑(amt<0)调一档,返回空格分隔 RGB 通道 */
function shade({ r, g, b }: { r: number; g: number; b: number }, amt: number): string {
  const f = (v: number) => Math.round(amt >= 0 ? v + (255 - v) * amt : v * (1 + amt))
  return `${f(r)} ${f(g)} ${f(b)}`
}

/** WCAG 相对亮度 */
function relLuminance({ r, g, b }: { r: number; g: number; b: number }): number {
  const f = (v: number) => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
  }
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
}

/** 给自定义主色配前景:在深墨与纸白中取 WCAG 对比度更高的一侧(亮度阈值法会给中等亮度色配出 <3:1 的前景) */
function readableFg(c: { r: number; g: number; b: number }): string {
  const bg = relLuminance(c)
  const contrast = (fg: number) => (Math.max(bg, fg) + 0.05) / (Math.min(bg, fg) + 0.05)
  const ink = relLuminance({ r: 20, g: 26, b: 30 })
  const paper = relLuminance({ r: 251, g: 248, b: 241 })
  return contrast(ink) >= contrast(paper) ? '20 26 30' : '251 248 241'
}

function applyAccent(accent: AccentKey, custom: string, isDark: boolean) {
  const s = document.documentElement.style
  // 内联样式优先级高于 tokens.css 的 :root 规则，从而覆盖默认强调色。
  if (accent === 'custom') {
    const c = hexToRgb(custom) ?? hexToRgb('#4A90C0')!
    const rgb = `${c.r} ${c.g} ${c.b}`
    s.setProperty('--c-accent', rgb)
    // 单一 hex 推导:明/暗主题分别朝白/黑微调出 hover
    s.setProperty('--c-accent-hover', shade(c, isDark ? 0.14 : -0.14))
    s.setProperty('--c-accent-fg', readableFg(c))
    s.setProperty('--wb-selection', rgb)
    return
  }
  const a = ACCENTS[accent] ?? ACCENTS.sky
  const v = isDark ? a.dark : a.light
  s.setProperty('--c-accent', v.base)
  s.setProperty('--c-accent-hover', v.hover)
  s.setProperty('--c-accent-fg', isDark ? a.fg.dark : a.fg.light)
  s.setProperty('--wb-selection', v.base)
}

function applyDensity(compact: boolean) {
  document.documentElement.setAttribute('data-density', compact ? 'compact' : 'comfortable')
}

function applyFontSize(px: FontSize) {
  document.documentElement.style.setProperty('--wb-font-size', `${px}px`)
}

/** 进程启动早期同步应用一次（避免首帧闪烁）。在 main.tsx 调用。 */
export function applyPrefsToDocument() {
  if (typeof document === 'undefined') return
  // URL ?theme= 覆盖（便于截图/分享特定主题）——仅作用于 DOM，不写回持久化偏好，避免永久篡改用户选择
  const url = new URLSearchParams(window.location.search).get('theme')
  const st = usePrefs.getState()
  const theme: ThemeMode = url === 'light' || url === 'dark' ? url : st.theme
  applyTheme(theme)
  applyAccent(st.accent, st.accentCustom, theme === 'dark')
  applyDensity(st.compact)
  applyFontSize(st.fontSize)
}

/* ───────────────────────── 跨窗口同步 ───────────────────────── */

// 仅这些「全局外观/代理」键在窗口间同步；纯主窗 UI 状态（搜索/详情宽度等）不广播。
const GLOBAL_KEYS: (keyof Prefs)[] = [
  'theme',
  'accent',
  'accentCustom',
  'compact',
  'fontSize',
  // follow 在独立「设置」窗口里也可改（自动滚动到最新），需实时同步回主窗口
  'follow',
  'systemProxy',
  'autoSystemProxy',
  'throttle',
  'port',
  'mitm',
  'scope',
  'upstream',
  'upstreamAddr',
  'maxFlows',
  'autoRecord',
  'runInBackground',
]

function globalSubset(s: Prefs): Partial<Prefs> {
  const out: Record<string, unknown> = {}
  for (const k of GLOBAL_KEYS) out[k] = s[k]
  return out as Partial<Prefs>
}

const PREFS_EVENT = 'prefs_changed'

/**
 * 在应用根挂载一次：负责把外观偏好落到 CSS 变量，并与其它窗口双向同步。
 */
export function usePrefsBridge() {
  const theme = usePrefs((s) => s.theme)
  const accent = usePrefs((s) => s.accent)
  const accentCustom = usePrefs((s) => s.accentCustom)
  const compact = usePrefs((s) => s.compact)
  const fontSize = usePrefs((s) => s.fontSize)

  useEffect(() => applyTheme(theme), [theme])
  useEffect(() => applyAccent(accent, accentCustom, theme === 'dark'), [accent, accentCustom, theme])
  useEffect(() => applyDensity(compact), [compact])
  useEffect(() => applyFontSize(fontSize), [fontSize])

  useEffect(() => {
    let applyingRemote = false
    let prevSig = JSON.stringify(globalSubset(usePrefs.getState()))

    const unsub = usePrefs.subscribe((state) => {
      if (applyingRemote) return
      const sig = JSON.stringify(globalSubset(state))
      if (sig === prevSig) return
      prevSig = sig
      try {
        void Events.Emit(PREFS_EVENT, globalSubset(state))
      } catch {
        /* 非 Wails 环境（浏览器预览）：忽略 */
      }
    })

    let off = () => {}
    try {
      off = Events.On(PREFS_EVENT, (e: { data?: Partial<Prefs> }) => {
        const patch = e?.data
        if (!patch) return
        applyingRemote = true
        try {
          usePrefs.getState().merge(patch)
        } finally {
          // 即使 merge/持久化抛错也要复位，否则本窗口将永久停止向外广播
          applyingRemote = false
          prevSig = JSON.stringify(globalSubset(usePrefs.getState()))
        }
      })
    } catch {
      /* ignore */
    }

    return () => {
      unsub()
      try {
        off()
      } catch {
        /* ignore */
      }
    }
  }, [])

  // 后端配置即时下发：监听后端相关偏好，变更即推送 updateConfig（去掉「保存」按钮）。
  // 仅主窗口执行——子窗口的改动会经上面的事件同步回主窗口，由主窗口统一下发，避免重复推送。
  //
  // 监听端口(port)会下发并持久化，但不即时重新绑定，需重启后生效（后端 ResolveListen）。
  // 主窗口启动时从后端拉取真实监听端口写回 prefs（单向），并广播给其它窗口。
  useEffect(() => {
    if (isStandalone) return

    Bridge.getListenInfo()
      .then((info) => {
        if (info) usePrefs.getState().set({ port: String(info.port) })
      })
      .catch(() => {})

    // 系统代理（运行时当前开关）由后端在启动时按「自动启用」决定，这里把后端的权威状态
    // 回读到 UI，避免开关显示与实际接管状态不一致（如旧版本遗留的本地偏好）。
    // 只在用户回读期间未手动改动对应键时才同步，避免抢点击时被还原。
    const persisted = {
      systemProxy: usePrefs.getState().systemProxy,
      autoSystemProxy: usePrefs.getState().autoSystemProxy,
      runInBackground: usePrefs.getState().runInBackground,
    }
    Bridge.getConfig()
      .then((cfg) => {
        if (!cfg) return
        const st = usePrefs.getState()
        const patch: Partial<Prefs> = {}
        for (const k of ['systemProxy', 'autoSystemProxy', 'runInBackground'] as const) {
          if (typeof cfg[k] === 'boolean' && st[k] === persisted[k] && cfg[k] !== persisted[k]) patch[k] = cfg[k]
        }
        if (Object.keys(patch).length) usePrefs.getState().set(patch)
      })
      .catch(() => {})

    let timer: ReturnType<typeof setTimeout> | undefined
    const push = (s: Prefs) => {
      const patch: Record<string, unknown> = {
        enableHTTPS: s.mitm,
        maxFlows: Number(s.maxFlows) || 5000,
        upstream: s.upstream,
        upstreamAddr: s.upstreamAddr,
        systemProxy: s.systemProxy,
        autoSystemProxy: s.autoSystemProxy,
        runInBackground: s.runInBackground,
      }
      // 端口仅在合法（1–65535）时下发，避免编辑中途的非法值覆盖持久化配置。
      const port = Number(s.port)
      if (Number.isInteger(port) && port >= 1 && port <= 65535) patch.port = port
      Bridge.updateConfig(patch).catch(() => {})
    }
    // 仅这些键变更才需下发；签名比对避免无关偏好（主题等）触发推送。
    const sig = (s: Prefs) =>
      JSON.stringify([
        s.port,
        s.mitm,
        s.maxFlows,
        s.upstream,
        s.upstreamAddr,
        s.systemProxy,
        s.autoSystemProxy,
        s.runInBackground,
      ])
    let prev = sig(usePrefs.getState())
    const unsub = usePrefs.subscribe((state) => {
      const next = sig(state)
      if (next === prev) return
      prev = next
      // 防抖：合并文本输入（上游地址）的连续按键，避免每次击键都下发。
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => push(state), 400)
    })
    return () => {
      unsub()
      // 卸载前把挂起的防抖推送补发一次，避免刚翻转的开关（如系统代理）在 400ms 内被丢弃。
      if (timer) {
        clearTimeout(timer)
        push(usePrefs.getState())
      }
    }
  }, [])
}
