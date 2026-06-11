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
export type AccentKey = 'iris' | 'sky' | 'teal' | 'amber' | 'rose'
export type BodyMode = 'tree' | 'raw' | 'hex'
export type DecryptScope = 'all' | 'allow' | 'deny'
export type FontSize = 12 | 13 | 14

export interface Prefs {
  // —— 全局外观（跨窗口同步） ——
  theme: ThemeMode
  accent: AccentKey
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

  // —— 代理 / 抓包（前端持有，后端接线后再下发） ——
  systemProxy: boolean
  throttle: boolean
  host: string
  port: string
  mitm: boolean
  scope: DecryptScope
  upstream: boolean
  upstreamAddr: string
  maxFlows: number
  autoRecord: boolean
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
  accent: 'iris',
  compact: false,
  fontSize: 13,
  searchVisible: false,
  follow: true,
  bodyMode: 'tree',
  detailWidth: 0,
  detailTopFrac: 0.45,
  systemProxy: true,
  throttle: false,
  host: '127.0.0.1',
  port: '8080',
  mitm: true,
  scope: 'all',
  upstream: false,
  upstreamAddr: '',
  maxFlows: 10000,
  autoRecord: true,
}

// 是否为独立子窗口（?w=settings|tools|about）。
const isStandalone =
  typeof window !== 'undefined' && !!new URLSearchParams(window.location.search).get('w')

// 「仅主窗口拥有」的 UI 键：不跨窗口同步、也只有主窗口会编辑。
// 独立子窗口持久化时必须保留 localStorage 中这些键的现值，
// 否则子窗口的整快照写会用自己的陈旧默认值覆盖主窗口刚写入的值（last-writer-wins）。
const UI_ONLY_KEYS: (keyof Prefs)[] = ['searchVisible', 'bodyMode', 'detailWidth', 'detailTopFrac']

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
      version: 1,
      storage: createJSONStorage(() => prefsStringStorage),
      // 只持久化数据字段（动作不入库）
      partialize: (s) => {
        const { set: _s, merge: _m, reset: _r, ...data } = s
        return data
      },
    },
  ),
)

/* ───────────────────────── 强调色定义 ───────────────────────── */

interface AccentDef {
  /** 深色主题下的 base / hover（空格分隔 RGB 通道，匹配 tokens.css 约定） */
  dark: { base: string; hover: string }
  /** 亮色主题下需更深以保证对比 */
  light: { base: string; hover: string }
  /** 强调底上的前景色（多数为白；琥珀用近黑） */
  fg: string
  /** 设置面板色板展示色 */
  swatch: string
}

export const ACCENTS: Record<AccentKey, AccentDef> = {
  iris: {
    dark: { base: '124 108 245', hover: '142 128 247' },
    light: { base: '99 84 224', hover: '86 71 214' },
    fg: '255 255 255',
    swatch: '#7C6CF5',
  },
  sky: {
    dark: { base: '56 132 255', hover: '96 165 250' },
    light: { base: '37 99 235', hover: '29 78 216' },
    fg: '255 255 255',
    swatch: '#3B82F6',
  },
  teal: {
    dark: { base: '20 184 166', hover: '45 212 191' },
    light: { base: '13 148 136', hover: '15 118 110' },
    fg: '255 255 255',
    swatch: '#14B8A6',
  },
  amber: {
    dark: { base: '245 166 35', hover: '247 183 80' },
    light: { base: '217 119 6', hover: '180 83 9' },
    fg: '26 22 14',
    swatch: '#F5A623',
  },
  rose: {
    dark: { base: '244 63 94', hover: '251 113 133' },
    light: { base: '225 29 72', hover: '190 18 60' },
    fg: '255 255 255',
    swatch: '#F43F5E',
  },
}

/* ───────────────────────── CSS 变量应用 ───────────────────────── */

function applyTheme(theme: ThemeMode) {
  document.documentElement.setAttribute('data-theme', theme)
}

function applyAccent(accent: AccentKey, isDark: boolean) {
  const a = ACCENTS[accent] ?? ACCENTS.iris
  const v = isDark ? a.dark : a.light
  const s = document.documentElement.style
  // 内联样式优先级高于 tokens.css 的 :root 规则，从而覆盖默认 iris。
  s.setProperty('--c-accent', v.base)
  s.setProperty('--c-accent-hover', v.hover)
  s.setProperty('--c-accent-fg', a.fg)
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
  applyAccent(st.accent, theme === 'dark')
  applyDensity(st.compact)
  applyFontSize(st.fontSize)
}

/* ───────────────────────── 跨窗口同步 ───────────────────────── */

// 仅这些「全局外观/代理」键在窗口间同步；纯主窗 UI 状态（搜索/详情宽度等）不广播。
const GLOBAL_KEYS: (keyof Prefs)[] = [
  'theme',
  'accent',
  'compact',
  'fontSize',
  // follow 在独立「设置」窗口里也可改（自动滚动到最新），需实时同步回主窗口
  'follow',
  'systemProxy',
  'throttle',
  'host',
  'port',
  'mitm',
  'scope',
  'upstream',
  'upstreamAddr',
  'maxFlows',
  'autoRecord',
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
  const compact = usePrefs((s) => s.compact)
  const fontSize = usePrefs((s) => s.fontSize)

  useEffect(() => applyTheme(theme), [theme])
  useEffect(() => applyAccent(accent, theme === 'dark'), [accent, theme])
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
  // 监听地址(host/port)刻意不在下发之列：它是启动期确定的部署设置，前端只读展示。
  // 主窗口启动时从后端拉取真实监听地址写回 prefs（单向），并广播给其它窗口。
  useEffect(() => {
    if (isStandalone) return

    Bridge.getListenInfo()
      .then((info) => {
        if (info) usePrefs.getState().set({ host: info.host, port: String(info.port) })
      })
      .catch(() => {})

    let timer: ReturnType<typeof setTimeout> | undefined
    const push = (s: Prefs) => {
      Bridge.updateConfig({
        enableHTTPS: s.mitm,
        maxFlows: Number(s.maxFlows) || 5000,
        upstream: s.upstream,
        upstreamAddr: s.upstreamAddr,
      }).catch(() => {})
    }
    // 仅这些键变更才需下发；签名比对避免无关偏好（主题、只读的 host/port）触发推送。
    const sig = (s: Prefs) => JSON.stringify([s.mitm, s.maxFlows, s.upstream, s.upstreamAddr])
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
      if (timer) clearTimeout(timer)
    }
  }, [])
}
