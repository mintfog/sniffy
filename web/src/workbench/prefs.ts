import { useEffect } from 'react'
import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'
import { Events } from '@wailsio/runtime'
import { Bridge } from '@/lib/bridge'
import {
  applyPalette,
  DEFAULT_CUSTOM_PALETTE,
  type CustomPalette,
  type PaletteKey,
  type ThemeMode,
} from './theme/palette'

/*
 * 工作台统一偏好（单一真相源 + 持久化 + 跨窗口同步）。
 *
 * 这里收口了原先散落各处的 UI 偏好：主题、配色方案、圆角、字体平滑、紧凑/字号、搜索框可见性、
 * 跟随最新、Body 查看模式、详情面板宽度/分栏、以及代理相关开关。全部写入
 * localStorage（key: sniffy-prefs），刷新/重启后保留。
 *
 * 桌面端是多窗口的（设置/工具箱/关于会弹出独立系统窗）。usePrefsBridge() 会：
 *   1. 把「全局外观类」偏好实时写进 :root 的属性与 CSS 变量（主题/配色方案/圆角/字体平滑/密度/字号）；
 *   2. 经 Wails 事件总线把全局偏好广播到其它窗口，使各窗口外观保持一致。
 */

export type { ThemeMode }
export type Corner = 'default' | 'large' | 'none'
export type BodyMode = 'tree' | 'raw' | 'hex'
export type DecryptScope = 'all' | 'allow' | 'deny'
export type FontSize = 12 | 13 | 14

export const DEFAULT_THROTTLE_KIBPS = 128
export const MIN_THROTTLE_KIBPS = 1
export const MAX_THROTTLE_KIBPS = 1024 * 1024

export interface Prefs {
  // —— 全局外观（跨窗口同步） ——
  theme: ThemeMode
  palette: PaletteKey
  /** palette==='custom' 时的种子色，亮、暗各一套；存用户原始输入，推导时再校正 */
  customPalette: CustomPalette
  corner: Corner
  fontSmoothing: boolean
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
  /** 每条连接每个方向的限速速率(KiB/s)。字符串便于数值输入框保留编辑中状态。 */
  throttleKiBps: string
  port: string
  mitm: boolean
  scope: DecryptScope
  /** allow 模式下仅解密这些主机;deny 模式下这些主机直通不解密。每行/逗号分隔一条,支持 * 通配。 */
  decryptAllow: string
  decryptDeny: string
  upstream: boolean
  upstreamAddr: string
  upstreamAuth: boolean
  upstreamUsername: string
  proxyAuth: boolean
  proxyUsername: string
  maxFlows: number
  autoRecord: boolean

  // —— 应用行为 ——
  skipCertificatePrompt: boolean
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
  palette: 'neutral',
  customPalette: DEFAULT_CUSTOM_PALETTE,
  corner: 'default',
  fontSmoothing: false,
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
  throttleKiBps: String(DEFAULT_THROTTLE_KIBPS),
  port: '8080',
  mitm: true,
  scope: 'all',
  decryptAllow: '',
  decryptDeny: '',
  upstream: false,
  upstreamAddr: '',
  upstreamAuth: false,
  upstreamUsername: '',
  proxyAuth: false,
  proxyUsername: '',
  maxFlows: 10000,
  autoRecord: true,
  skipCertificatePrompt: false,
  runInBackground: true,
}

/** 把用户输入的主机清单（换行/逗号分隔）规整为去空白、去空项的数组,供下发后端。 */
function splitHosts(s: string): string[] {
  return s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean)
}

/**
 * 去掉代理地址中内嵌的 `user:pass@`，只动 authority 段（不补默认端口、不加尾斜杠），
 * 避免归一化出一个与后端不一致的地址而触发多余的下发。
 */
function stripUpstreamUserinfo(addr: string): string {
  const trimmed = addr.trim()
  if (!trimmed) return trimmed
  const scheme = trimmed.indexOf('://')
  const start = scheme >= 0 ? scheme + 3 : 0
  const rest = trimmed.slice(start)
  const sep = rest.search(/[/?#]/)
  const authority = sep >= 0 ? rest.slice(0, sep) : rest
  const at = authority.lastIndexOf('@')
  if (at >= 0) return trimmed.slice(0, start) + authority.slice(at + 1) + (sep >= 0 ? rest.slice(sep) : '')
  // 密码里未转义的 /?# 会把 authority 提前截断，凭据落在后面那段。只在地址本身解析不了时
  // 才退到「整段取最后一个 @」——合法地址路径里的 @ 不能被当成凭据。
  try {
    new URL(scheme >= 0 ? trimmed : `http://${trimmed}`)
    return trimmed
  } catch {
    const late = rest.lastIndexOf('@')
    return late < 0 ? trimmed : trimmed.slice(0, start) + rest.slice(late + 1)
  }
}

/**
 * 地址是否已成型到可以下发。刚敲下 `user:pass@`、host 还没出现时下发，后端会把这半截
 * 凭据迁进独立字段，再把只剩 `http://` 的地址回灌到输入框。
 */
function upstreamAddrReady(addr: string): boolean {
  const trimmed = addr.trim()
  if (!trimmed) return true
  const stripped = stripUpstreamUserinfo(trimmed)
  const scheme = stripped.indexOf('://')
  const rest = scheme >= 0 ? stripped.slice(scheme + 3) : stripped
  const sep = rest.search(/[/?#]/)
  return (sep >= 0 ? rest.slice(0, sep) : rest).length > 0
}

// 是否为独立子窗口（?w=settings|tools|about）。
const isStandalone =
  typeof window !== 'undefined' && !!new URLSearchParams(window.location.search).get('w')

// 「仅主窗口拥有」的 UI 键：不跨窗口同步、也只有主窗口会编辑。
// 独立子窗口持久化时必须保留 localStorage 中这些键的现值，
// 否则子窗口的整快照写会用自己的陈旧默认值覆盖主窗口刚写入的值（last-writer-wins）。
const UI_ONLY_KEYS: (keyof Prefs)[] = [
  'searchVisible',
  'bodyMode',
  'detailWidth',
  'detailTopFrac',
  'lanIP',
  'skipCertificatePrompt',
]

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
      version: 5,
      storage: createJSONStorage(() => prefsStringStorage),
      // 只持久化数据字段（动作不入库）
      partialize: (s) => {
        const { set: _s, merge: _m, reset: _r, ...data } = s
        void _s
        void _m
        void _r
        return data
      },
      migrate: (persisted, version) => {
        const p = persisted as (Partial<Prefs> & LegacyAccentPrefs) | undefined
        if (!p || typeof p !== 'object') return p
        if (version < 3) {
          if (p.theme !== 'dark' && p.theme !== 'light') p.theme = 'dark'
        }
        if (version < 4) {
          // 旧版本把上游代理凭据内嵌在地址里，这份副本一直留在 localStorage：不剥离的话
          // 界面会持续显示明文密码，还会把它推回后端。
          if (typeof p.upstreamAddr === 'string') p.upstreamAddr = stripUpstreamUserinfo(p.upstreamAddr)
        }
        if (version < 5) migrateAccentToPalette(p)
        return p
      },
    },
  ),
)

/* ───────────────────────── 旧强调色迁移 ───────────────────────── */

interface LegacyAccentPrefs {
  accent?: string
  accentCustom?: string
}

const LEGACY_ACCENT_PALETTE: Record<string, PaletteKey> = {
  sky: 'cobalt',
  prussian: 'cobalt',
  teal: 'sage',
  brass: 'amber',
  slate: 'graphite',
  vermilion: 'neutral',
}

// 「强调色」由「配色方案」取代：旧的自定义 hex 以中性方案为底、只换主色
function migrateAccentToPalette(p: Partial<Prefs> & LegacyAccentPrefs) {
  if (p.accent === 'custom' && typeof p.accentCustom === 'string' && /^#[0-9a-f]{6}$/i.test(p.accentCustom)) {
    p.palette = 'custom'
    p.customPalette = {
      light: { ...DEFAULT_CUSTOM_PALETTE.light, primary: p.accentCustom },
      dark: { ...DEFAULT_CUSTOM_PALETTE.dark, primary: p.accentCustom },
    }
  } else {
    p.palette = LEGACY_ACCENT_PALETTE[p.accent ?? ''] ?? 'neutral'
  }
  delete p.accent
  delete p.accentCustom
}

function parseThrottleKiBps(value: string): number | undefined {
  const n = Number(value)
  return Number.isInteger(n) && n >= MIN_THROTTLE_KIBPS && n <= MAX_THROTTLE_KIBPS ? n : undefined
}

export function normalizeThrottleKiBps(value: string): string {
  if (!value.trim()) return String(DEFAULT_THROTTLE_KIBPS)
  const n = Number(value)
  if (!Number.isFinite(n)) return String(DEFAULT_THROTTLE_KIBPS)
  return String(Math.min(MAX_THROTTLE_KIBPS, Math.max(MIN_THROTTLE_KIBPS, Math.round(n))))
}

/* ───────────────────────── CSS 变量应用 ───────────────────────── */

function applyTheme(theme: ThemeMode) {
  document.documentElement.setAttribute('data-theme', theme)
}

/** 实际生效的主题：URL ?theme= 覆盖（便于截图/分享）只作用于 DOM，不写回持久化偏好。 */
export function effectiveTheme(theme: ThemeMode): ThemeMode {
  const url = new URLSearchParams(window.location.search).get('theme')
  return url === 'light' || url === 'dark' ? url : theme
}

function applyCorner(corner: Corner) {
  if (corner === 'default') document.documentElement.removeAttribute('data-corner')
  else document.documentElement.setAttribute('data-corner', corner)
}

function applyFontSmoothing(on: boolean) {
  if (on) document.documentElement.setAttribute('data-font-smoothing', 'antialiased')
  else document.documentElement.removeAttribute('data-font-smoothing')
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
  const st = usePrefs.getState()
  const theme = effectiveTheme(st.theme)
  applyTheme(theme)
  applyPalette(st.palette, st.customPalette, theme)
  applyCorner(st.corner)
  applyFontSmoothing(st.fontSmoothing)
  applyDensity(st.compact)
  applyFontSize(st.fontSize)
}

/* ───────────────────────── 跨窗口同步 ───────────────────────── */

// 仅这些「全局外观/代理」键在窗口间同步；纯主窗 UI 状态（搜索/详情宽度等）不广播。
const GLOBAL_KEYS: (keyof Prefs)[] = [
  'theme',
  'palette',
  'customPalette',
  'corner',
  'fontSmoothing',
  'compact',
  'fontSize',
  // follow 在独立「设置」窗口里也可改（自动滚动到最新），需实时同步回主窗口
  'follow',
  'systemProxy',
  'autoSystemProxy',
  'throttle',
  'throttleKiBps',
  'port',
  'mitm',
  'scope',
  'decryptAllow',
  'decryptDeny',
  'upstream',
  'upstreamAddr',
  'upstreamAuth',
  'upstreamUsername',
  'proxyAuth',
  'proxyUsername',
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
  const palette = usePrefs((s) => s.palette)
  const customPalette = usePrefs((s) => s.customPalette)
  const corner = usePrefs((s) => s.corner)
  const fontSmoothing = usePrefs((s) => s.fontSmoothing)
  const compact = usePrefs((s) => s.compact)
  const fontSize = usePrefs((s) => s.fontSize)

  useEffect(() => applyTheme(effectiveTheme(theme)), [theme])
  useEffect(() => applyPalette(palette, customPalette, effectiveTheme(theme)), [palette, customPalette, theme])
  useEffect(() => applyCorner(corner), [corner])
  useEffect(() => applyFontSmoothing(fontSmoothing), [fontSmoothing])
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
      upstream: usePrefs.getState().upstream,
      upstreamAddr: usePrefs.getState().upstreamAddr,
      upstreamAuth: usePrefs.getState().upstreamAuth,
      upstreamUsername: usePrefs.getState().upstreamUsername,
      proxyAuth: usePrefs.getState().proxyAuth,
      proxyUsername: usePrefs.getState().proxyUsername,
      systemProxy: usePrefs.getState().systemProxy,
      autoSystemProxy: usePrefs.getState().autoSystemProxy,
      throttle: usePrefs.getState().throttle,
      throttleKiBps: usePrefs.getState().throttleKiBps,
      runInBackground: usePrefs.getState().runInBackground,
    }
    Bridge.getConfig()
      .then((cfg) => {
        if (!cfg) return
        const st = usePrefs.getState()
        const patch: Partial<Prefs> = {}
        for (const k of ['upstream', 'upstreamAuth', 'proxyAuth', 'systemProxy', 'autoSystemProxy', 'throttle', 'runInBackground'] as const) {
          if (typeof cfg[k] === 'boolean' && st[k] === persisted[k] && cfg[k] !== persisted[k]) patch[k] = cfg[k]
        }
        for (const k of ['upstreamAddr', 'upstreamUsername', 'proxyUsername'] as const) {
          if (typeof cfg[k] === 'string' && st[k] === persisted[k] && cfg[k] !== persisted[k]) patch[k] = cfg[k]
        }
        if (
          typeof cfg.throttleKiBps === 'number' &&
          st.throttleKiBps === persisted.throttleKiBps &&
          String(cfg.throttleKiBps) !== persisted.throttleKiBps
        ) {
          patch.throttleKiBps = String(cfg.throttleKiBps)
        }
        if (Object.keys(patch).length) usePrefs.getState().set(patch)
      })
      .catch(() => {})

    const backendKeys = [
      'port',
      'mitm',
      'maxFlows',
      'upstream',
      'upstreamAddr',
      'upstreamAuth',
      'upstreamUsername',
      'proxyUsername',
      'systemProxy',
      'autoSystemProxy',
      'throttle',
      'throttleKiBps',
      'runInBackground',
      'scope',
      'decryptAllow',
      'decryptDeny',
    ] as const
    type BackendKey = (typeof backendKeys)[number]
    const snapshot = (s: Prefs): Record<BackendKey, unknown> => {
      const out = {} as Record<BackendKey, unknown>
      for (const k of backendKeys) out[k] = s[k]
      return out
    }
    let timer: ReturnType<typeof setTimeout> | undefined
    const pending = new Set<BackendKey>()
    const push = (s: Prefs, changed: ReadonlySet<BackendKey>) => {
      const patch: Record<string, unknown> = {}
      if (changed.has('mitm')) patch.enableHTTPS = s.mitm
      if (changed.has('maxFlows')) patch.maxFlows = Number(s.maxFlows) || 5000
      if (changed.has('upstream')) patch.upstream = s.upstream
      if (changed.has('upstreamAddr') && upstreamAddrReady(s.upstreamAddr)) patch.upstreamAddr = s.upstreamAddr
      if (changed.has('upstreamAuth')) patch.upstreamAuth = s.upstreamAuth
      if (changed.has('upstreamUsername')) patch.upstreamUsername = s.upstreamUsername
      // proxyAuth 不在这里下发（也不在 backendKeys 里）：认证必须与账号密码成套提交，
      // 由 SettingsView 负责。账号同理只在非空时发——监听端对凭据不全是 fail-closed，
      // 清空输入框的一瞬间发出空账号，会把所有客户端打成 407。
      if (changed.has('proxyUsername') && s.proxyUsername !== '') patch.proxyUsername = s.proxyUsername
      if (changed.has('systemProxy')) patch.systemProxy = s.systemProxy
      if (changed.has('autoSystemProxy')) patch.autoSystemProxy = s.autoSystemProxy
      if (changed.has('throttle')) patch.throttle = s.throttle
      if (changed.has('runInBackground')) patch.runInBackground = s.runInBackground
      if (changed.has('scope')) patch.decryptScope = s.scope
      if (changed.has('decryptAllow')) patch.decryptAllow = splitHosts(s.decryptAllow)
      if (changed.has('decryptDeny')) patch.decryptDeny = splitHosts(s.decryptDeny)
      // 端口仅在合法（1–65535）时下发，避免编辑中途的非法值覆盖持久化配置。
      if (changed.has('port')) {
        const port = Number(s.port)
        if (Number.isInteger(port) && port >= 1 && port <= 65535) patch.port = port
      }
      if (changed.has('throttleKiBps')) {
        const throttleKiBps = parseThrottleKiBps(s.throttleKiBps)
        if (throttleKiBps !== undefined) patch.throttleKiBps = throttleKiBps
      }
      if (!Object.keys(patch).length) return
      // 后端会规范化上游配置（剥离地址内嵌的凭据并迁移到独立认证字段），不回灌的话界面
      // 会一直显示带密码的旧地址。只回灌这期间用户没再改动过的键，避免覆盖正在输入的内容。
      const sent = {
        upstreamAddr: s.upstreamAddr,
        upstreamAuth: s.upstreamAuth,
        upstreamUsername: s.upstreamUsername,
        proxyAuth: s.proxyAuth,
        proxyUsername: s.proxyUsername,
        systemProxy: s.systemProxy,
      }
      Bridge.updateConfig(patch)
        .then((cfg) => {
          if (!cfg) return
          const now = usePrefs.getState()
          const back: Partial<Prefs> = {}
          // 后端拿不准的地址会回空串，直接回灌就把用户正在输入的内容清掉了。
          const addrOK = cfg.upstreamAddr !== '' || sent.upstreamAddr === ''
          if (typeof cfg.upstreamAddr === 'string' && addrOK && now.upstreamAddr === sent.upstreamAddr && cfg.upstreamAddr !== sent.upstreamAddr) {
            back.upstreamAddr = cfg.upstreamAddr
          }
          if (typeof cfg.upstreamAuth === 'boolean' && now.upstreamAuth === sent.upstreamAuth && cfg.upstreamAuth !== sent.upstreamAuth) {
            back.upstreamAuth = cfg.upstreamAuth
          }
          if (typeof cfg.upstreamUsername === 'string' && now.upstreamUsername === sent.upstreamUsername && cfg.upstreamUsername !== sent.upstreamUsername) {
            back.upstreamUsername = cfg.upstreamUsername
          }
          if (typeof cfg.proxyAuth === 'boolean' && now.proxyAuth === sent.proxyAuth && cfg.proxyAuth !== sent.proxyAuth) {
            back.proxyAuth = cfg.proxyAuth
          }
          if (typeof cfg.proxyUsername === 'string' && now.proxyUsername === sent.proxyUsername && cfg.proxyUsername !== sent.proxyUsername) {
            back.proxyUsername = cfg.proxyUsername
          }
          // 接管失败时后端会把开关改回关闭；回灌后的这次关闭推送也会触发后端清理未撤销干净的残留设置。
          if (typeof cfg.systemProxy === 'boolean' && now.systemProxy === sent.systemProxy && cfg.systemProxy !== sent.systemProxy) {
            back.systemProxy = cfg.systemProxy
          }
          if (Object.keys(back).length) usePrefs.getState().set(back)
        })
        .catch(() => {})
    }
    let prev = snapshot(usePrefs.getState())
    const unsub = usePrefs.subscribe((state) => {
      const next = snapshot(state)
      for (const k of backendKeys) {
        if (next[k] !== prev[k]) pending.add(k)
      }
      prev = next
      if (!pending.size) return
      // 防抖：合并文本输入（上游地址）的连续按键，避免每次击键都下发。
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => {
        timer = undefined
        const changed = new Set(pending)
        pending.clear()
        push(usePrefs.getState(), changed)
      }, 400)
    })
    return () => {
      unsub()
      // 卸载前把挂起的防抖推送补发一次，避免刚翻转的开关（如系统代理）在 400ms 内被丢弃。
      if (timer) {
        clearTimeout(timer)
        timer = undefined
        const changed = new Set(pending)
        pending.clear()
        push(usePrefs.getState(), changed)
      }
    }
  }, [])
}
