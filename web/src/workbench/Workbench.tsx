import {
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import {
  Binary,
  Braces,
  CircleDot,
  Code2,
  Copy,
  Download,
  Eye,
  EyeOff,
  FileDown,
  FileJson,
  Fingerprint,
  Gauge,
  Highlighter,
  Info,
  KeyRound,
  ListChecks,
  Puzzle,
  QrCode,
  RefreshCw,
  Send,
  ShieldCheck,
  Shuffle,
  Terminal,
  Trash2,
  Wand2,
} from 'lucide-react'
import { Events } from '@wailsio/runtime'
import { useTranslation } from 'react-i18next'
import { useAppStore, useSystemStatus } from '@/store'
import { Bridge } from '@/lib/bridge'
import './theme/tokens.css'
import { useTheme } from './theme/useTheme'
import { usePrefs } from './prefs'
import { useTraffic } from './data/useTraffic'
import { useBackendSync } from './data/useBackendSync'
import type { MarkColor, TrafficRow } from './lib/types'
import { buildCurl, copyText, headersToText } from './lib/clipboard'
import { exportHar, exportJson } from './lib/exporters'
import { saveFile } from './lib/download'
import { DOCS_URL, openExternal } from './lib/links'
import { openAboutWindow, openPluginsWindow, openSettingsWindow, openToolboxWindow } from './lib/windows'
import { TitleBar } from './shell/TitleBar'
import { useNativeMenu } from './shell/nativeMenu'
import { IconRail, type WorkbenchView } from './shell/IconRail'
import { ProxyBar } from './shell/ProxyBar'
import { Toolbar, type FilterChip } from './shell/Toolbar'
import { StatusBar } from './shell/StatusBar'
import { TrafficTable } from './views/TrafficTable'
import { FindScope } from './views/FindScope'
import { DetailPanel } from './views/DetailPanel'
import { WsDetailPanel } from './views/WsDetailPanel'
import { StreamDetailPanel } from './views/StreamDetailPanel'
import { SettingsView } from './views/SettingsView'
import { RulesView } from './views/RulesView'
import { BreakpointsView } from './views/BreakpointsView'
import { PluginsView } from './views/PluginsView'
import { CertsView } from './views/CertsView'
import { ContextMenu, type MenuNode, type TopMenu } from './ui/Menu'

/** 代理监听地址未知内网 IP 时的回退主机 */
const FALLBACK_HOST = '127.0.0.1'
const DETAIL_MIN = 380

type ChipKey = 'all' | 'https' | 'http' | 'ws' | 'json' | 'image' | 'err'

/** 详情面板宽度上限：随窗口收缩，给左侧流量表留出最小空间。 */
function maxDetailWidth(): number {
  return Math.max(DETAIL_MIN, window.innerWidth - 420)
}
function clampDetail(w: number): number {
  return Math.min(maxDetailWidth(), Math.max(DETAIL_MIN, w))
}

/** 高亮标记的菜单选项（颜色 + 快捷键，参考竞品）；labelKey 在组件内经 t() 解析以随语言更新。 */
const MARK_OPTIONS: { color: MarkColor; labelKey: string; swatch: string; shortcut: string }[] = [
  { color: 'red', labelKey: 'workbench.mark.red', swatch: 'bg-rose-500', shortcut: 'Alt+1' },
  { color: 'yellow', labelKey: 'workbench.mark.yellow', swatch: 'bg-amber-400', shortcut: 'Alt+2' },
  { color: 'green', labelKey: 'workbench.mark.green', swatch: 'bg-emerald-500', shortcut: 'Alt+3' },
  { color: 'blue', labelKey: 'workbench.mark.blue', swatch: 'bg-sky-500', shortcut: 'Alt+4' },
  { color: 'cyan', labelKey: 'workbench.mark.cyan', swatch: 'bg-cyan-400', shortcut: 'Alt+5' },
]

// 用 e.code（物理键）匹配：macOS 上 Option+数字的 e.key 是特殊字符（¡™£…），用 e.key 会失效
const MARK_BY_CODE: Record<string, MarkColor> = {
  Digit1: 'red',
  Digit2: 'yellow',
  Digit3: 'green',
  Digit4: 'blue',
  Digit5: 'cyan',
}

/** 焦点是否在输入控件里（此时不劫持 Ctrl+A / Delete / 方向键） */
function isTypingTarget(): boolean {
  const el = document.activeElement as HTMLElement | null
  return !!el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)
}

function matchChip(row: TrafficRow, key: ChipKey): boolean {
  switch (key) {
    case 'all': return true
    case 'https': return row.scheme === 'https'
    case 'http': return row.scheme === 'http'
    case 'ws': return row.kind === 'ws'
    case 'json': return row.contentKind === 'json'
    case 'image': return row.contentKind === 'image'
    case 'err': return row.blocked === true || row.state === 'error' || (!!row.status && row.status >= 400)
  }
}

const NAV_VIEWS: WorkbenchView[] = ['traffic', 'rules', 'breakpoints', 'plugins', 'certs', 'settings']

export default function Workbench() {
  const { t } = useTranslation()
  useBackendSync() // 连接 Wails v3 后端：回填会话 + 订阅实时事件 + 录制状态
  const { isDark, toggle: toggleTheme } = useTheme()
  const { rows, removeRows } = useTraffic()
  const { isConnected } = useSystemStatus()
  const storeRecording = useAppStore((s) => s.isRecording)
  const setStoreRecording = useAppStore((s) => s.setRecording)
  const clearAllData = useAppStore((s) => s.clearAllData)

  // —— 持久化偏好 ——
  const follow = usePrefs((s) => s.follow)
  const systemProxy = usePrefs((s) => s.systemProxy)
  const autoSystemProxy = usePrefs((s) => s.autoSystemProxy)
  const throttle = usePrefs((s) => s.throttle)
  const searchVisible = usePrefs((s) => s.searchVisible)
  const prefDetailWidth = usePrefs((s) => s.detailWidth)
  const port = usePrefs((s) => s.port)
  const setPref = usePrefs((s) => s.set)

  // 代理监听地址展示用本机内网 IP（向后端取，便于同网段设备指向本机）；
  // 非 Wails 预览或取不到时回退回环地址。端口跟随偏好，改端口即时反映。
  const [lanIP, setLanIP] = useState(FALLBACK_HOST)
  useEffect(() => {
    Bridge.getLanIP()
      .then((ip) => { if (ip) setLanIP(ip) })
      .catch(() => {})
  }, [])
  const proxyAddr = `${lanIP}:${port}`

  const [view, setView] = useState<WorkbenchView>(() => {
    const v = new URLSearchParams(window.location.search).get('view')
    return (NAV_VIEWS.includes((v ?? '') as WorkbenchView) ? v : 'traffic') as WorkbenchView
  })
  const [chip, setChip] = useState<ChipKey>('all')
  const [search, setSearch] = useState('')
  /* 选择模型：focusedId = 详情面板展示的焦点行；selectedIds = 多选集合（含焦点行） */
  const [focusedId, setFocusedId] = useState<string>()
  const [selectedIds, setSelectedIds] = useState<ReadonlySet<string>>(() => new Set())
  /** Shift 范围选择的锚点行 */
  const anchorRef = useRef<string>()
  /** 已查看（已阅）的行，列表中置灰 */
  const [readIds, setReadIds] = useState<ReadonlySet<string>>(() => new Set())
  /** 行高亮标记（右键 → 高亮） */
  const [marks, setMarks] = useState<Partial<Record<string, MarkColor>>>({})
  /** 右键菜单：屏幕坐标 + 触发行 */
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; rowId: string } | null>(null)
  /** 详情面板宽度：默认窗口一半；持久化于偏好（0 表示尚未自定义）。 */
  const [detailWidth, setDetailWidth] = useState(() =>
    clampDetail(prefDetailWidth > 0 ? prefDetailWidth : Math.round(window.innerWidth * 0.5)),
  )

  const searchRef = useRef<HTMLInputElement>(null)
  const capturing = storeRecording

  // 打开设置：优先独立系统窗口；非 Wails（浏览器预览）回退到主窗内嵌视图。
  const openSettings = useCallback(() => {
    openSettingsWindow().catch(() => setView('settings'))
  }, [])

  // 打开插件工作室：同样优先独立窗口（编辑 + 日志同屏更宽敞），回退到主窗内嵌视图。
  const openPlugins = useCallback(() => {
    openPluginsWindow().catch(() => setView('plugins'))
  }, [])

  // 统一导航：设置 / 插件走独立窗口，其余切换主窗视图。
  const handleNav = useCallback(
    (v: WorkbenchView) => {
      if (v === 'settings') openSettings()
      else if (v === 'plugins') openPlugins()
      else setView(v)
    },
    [openSettings, openPlugins],
  )

  /* ── 过滤 ── */
  const counts = useMemo(() => {
    const c: Record<ChipKey, number> = { all: rows.length, https: 0, http: 0, ws: 0, json: 0, image: 0, err: 0 }
    for (const r of rows) {
      if (r.scheme === 'https') c.https++
      if (r.scheme === 'http') c.http++
      if (r.kind === 'ws') c.ws++
      if (r.contentKind === 'json') c.json++
      if (r.contentKind === 'image') c.image++
      if (r.blocked || r.state === 'error' || (r.status && r.status >= 400)) c.err++
    }
    return c
  }, [rows])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter((r) => {
      if (!matchChip(r, chip)) return false
      if (!q) return true
      return (
        r.url.toLowerCase().includes(q) ||
        r.host.toLowerCase().includes(q) ||
        r.method.toLowerCase().includes(q) ||
        (r.status ? String(r.status).includes(q) : false) ||
        (r.process ? r.process.toLowerCase().includes(q) : false)
      )
    })
  }, [rows, chip, search])

  const focusedRow = useMemo(() => rows.find((r) => r.id === focusedId), [rows, focusedId])
  // 流式 Flow(SSE / gRPC / 分块流)以普通 HTTP 行展示;若该行有对应的流会话,详情面板换成消息时间线。
  const focusedHasStream = useAppStore((s) => !!focusedId && s.streamSessions.some((x) => x.id === focusedId))
  const selectedRows = useMemo(() => rows.filter((r) => selectedIds.has(r.id)), [rows, selectedIds])

  // 用 ref 暴露「最新值」给菜单/快捷键动作，使这些回调保持引用稳定。
  // 否则流量每 ~700ms 刷新 → filtered/removeRows 变化 → 重建 menus → 顶部菜单整条重渲染，
  // 开着的下拉会闪、难以操作（用户反馈「数据刷新时菜单跟着刷新，无法查看」）。
  const filteredRef = useRef(filtered)
  filteredRef.current = filtered
  const selectedIdsRef = useRef(selectedIds)
  selectedIdsRef.current = selectedIds
  const focusedIdRef = useRef(focusedId)
  focusedIdRef.current = focusedId
  const removeRowsRef = useRef(removeRows)
  removeRowsRef.current = removeRows

  /* ── 选择 ── */
  const selectSingle = useCallback((id: string) => {
    setSelectedIds(new Set([id]))
    setFocusedId(id)
    anchorRef.current = id
  }, [])

  const clearSelection = useCallback(() => {
    setSelectedIds(new Set())
    setFocusedId(undefined)
    anchorRef.current = undefined
  }, [])

  // 拖拽框选：仅更新多选集合 + 锚点（不动焦点行）
  const handleMarqueeSelect = useCallback((ids: ReadonlySet<string>, anchorId?: string) => {
    setSelectedIds(ids)
    if (anchorId) anchorRef.current = anchorId
  }, [])

  // 框选收尾：焦点行已不在选中集合内则清掉（关闭详情面板）
  const handleMarqueeEnd = useCallback(() => {
    setFocusedId((f) => (f && selectedIdsRef.current.has(f) ? f : undefined))
  }, [])

  const selectAll = useCallback(() => {
    setSelectedIds(new Set(filteredRef.current.map((r) => r.id)))
  }, [])

  const invertSelection = useCallback(() => {
    const f = filteredRef.current
    const sel = selectedIdsRef.current
    const next = new Set(f.filter((r) => !sel.has(r.id)).map((r) => r.id))
    setSelectedIds(next)
    // 焦点行/锚点被反选掉时同步清掉，避免详情面板展示未选中行、Shift 范围从陈旧锚点起算
    setFocusedId((cur) => (cur && next.has(cur) ? cur : undefined))
    if (anchorRef.current && !next.has(anchorRef.current)) anchorRef.current = undefined
  }, [])

  // 过滤/搜索变化后把选择集收敛到可见行：批量删除/标记永远不会命中已隐藏的行
  useEffect(() => {
    if (selectedIds.size === 0 && !focusedId && !anchorRef.current) return
    const visible = new Set(filtered.map((r) => r.id))
    setSelectedIds((prev) => {
      let changed = false
      const next = new Set<string>()
      for (const id of prev) {
        if (visible.has(id)) next.add(id)
        else changed = true
      }
      return changed ? next : prev
    })
    setFocusedId((f) => (f && !visible.has(f) ? undefined : f))
    if (anchorRef.current && !visible.has(anchorRef.current)) anchorRef.current = undefined
  }, [filtered, selectedIds, focusedId])

  // 已阅/标记按「存活行」回收（行被删除后清理，防止无界增长）；
  // 注意不能按「可见行」收敛——切换筛选不应抹掉已阅/标记状态
  useEffect(() => {
    if (readIds.size === 0 && Object.keys(marks).length === 0) return
    const alive = new Set(rows.map((r) => r.id))
    setReadIds((prev) => {
      let changed = false
      const next = new Set<string>()
      for (const id of prev) {
        if (alive.has(id)) next.add(id)
        else changed = true
      }
      return changed ? next : prev
    })
    setMarks((prev) => {
      const keys = Object.keys(prev)
      if (keys.every((k) => alive.has(k))) return prev
      const next: Partial<Record<string, MarkColor>> = {}
      for (const k of keys) if (alive.has(k)) next[k] = prev[k]
      return next
    })
  }, [rows, readIds, marks])

  const handleRowClick = useCallback(
    (row: TrafficRow, e: ReactMouseEvent) => {
      if (e.shiftKey && anchorRef.current) {
        const ai = filtered.findIndex((r) => r.id === anchorRef.current)
        const bi = filtered.findIndex((r) => r.id === row.id)
        if (ai >= 0 && bi >= 0) {
          const [lo, hi] = ai < bi ? [ai, bi] : [bi, ai]
          setSelectedIds(new Set(filtered.slice(lo, hi + 1).map((r) => r.id)))
          setFocusedId(row.id)
          return
        }
        selectSingle(row.id)
      } else if (e.ctrlKey || e.metaKey) {
        const willDeselect = selectedIds.has(row.id)
        setSelectedIds((prev) => {
          const next = new Set(prev)
          if (next.has(row.id)) next.delete(row.id)
          else next.add(row.id)
          return next
        })
        if (willDeselect) {
          // 取消选中的若是焦点行，则清焦点（详情面板关闭），避免「展示中却未选中」的矛盾态
          setFocusedId((f) => (f === row.id ? undefined : f))
        } else {
          setFocusedId(row.id)
          anchorRef.current = row.id
        }
      } else {
        selectSingle(row.id)
      }
    },
    [filtered, selectedIds, selectSingle],
  )

  // 焦点行（详情面板已展示）自动标记为已阅
  useEffect(() => {
    if (!focusedId) return
    setReadIds((prev) => {
      if (prev.has(focusedId)) return prev
      const next = new Set(prev)
      next.add(focusedId)
      return next
    })
  }, [focusedId])

  /* ── 标记 / 已阅 ── */
  /** 批量操作的目标：多选集合，否则焦点行（读 ref 以保持回调引用稳定） */
  const targetIds = useCallback((): string[] => {
    const sel = selectedIdsRef.current
    if (sel.size > 0) return [...sel]
    return focusedIdRef.current ? [focusedIdRef.current] : []
  }, [])

  const setMarkFor = useCallback(
    (color?: MarkColor) => {
      const ids = targetIds()
      if (!ids.length) return
      setMarks((prev) => {
        const next = { ...prev }
        for (const id of ids) {
          if (color) next[id] = color
          else delete next[id]
        }
        return next
      })
    },
    [targetIds],
  )

  const setReadFor = useCallback(
    (read: boolean) => {
      const ids = targetIds()
      if (!ids.length) return
      setReadIds((prev) => {
        const next = new Set(prev)
        for (const id of ids) {
          if (read) next.add(id)
          else next.delete(id)
        }
        return next
      })
    },
    [targetIds],
  )

  /* ── 删除 ── */
  const deleteSelected = useCallback(() => {
    const ids = new Set(targetIds())
    if (!ids.size) return
    removeRowsRef.current(ids)
    setSelectedIds(new Set())
    setFocusedId(undefined)
    anchorRef.current = undefined
    setCtxMenu(null)
    setReadIds((prev) => {
      const next = new Set(prev)
      for (const id of ids) next.delete(id)
      return next
    })
    setMarks((prev) => {
      const next = { ...prev }
      for (const id of ids) delete next[id]
      return next
    })
  }, [targetIds])

  /* ── 复制 ── */
  const copyFromRows = useCallback(
    (pick: (r: TrafficRow) => string | undefined) => {
      const list = selectedRows.length > 0 ? selectedRows : focusedRow ? [focusedRow] : []
      const text = list.map(pick).filter(Boolean).join('\n')
      if (text) void copyText(text)
    },
    [selectedRows, focusedRow],
  )

  const chips: FilterChip[] = [
    { key: 'all', label: t('workbench.filter.all'), count: counts.all },
    { key: 'https', label: 'HTTPS', count: counts.https },
    { key: 'http', label: 'HTTP', count: counts.http },
    { key: 'ws', label: 'WS', count: counts.ws },
    { key: 'json', label: 'JSON', count: counts.json },
    { key: 'image', label: t('workbench.filter.image'), count: counts.image },
    { key: 'err', label: t('workbench.filter.error'), count: counts.err },
  ]

  /* ── 动作 ── */
  // 暂停/继续「捕获」：端口始终监听，这里只控制是否把新流量记入表格。
  const toggleCapture = useCallback(() => {
    const next = !storeRecording
    setStoreRecording(next) // 乐观更新；后端无录制状态推送
    void (next ? Bridge.startRecording() : Bridge.stopRecording()).catch(() => {})
  }, [setStoreRecording, storeRecording])

  const clear = useCallback(() => {
    setSelectedIds(new Set())
    setFocusedId(undefined)
    anchorRef.current = undefined
    setReadIds(new Set())
    setMarks({})
    setCtxMenu(null)
    clearAllData()
    void Bridge.clearSessions().catch(() => {})
  }, [clearAllData])

  /** 仅清空本窗口的本地状态（响应子窗口「清空」事件，后端已由发起方清过） */
  const clearLocal = useCallback(() => {
    setSelectedIds(new Set())
    setFocusedId(undefined)
    anchorRef.current = undefined
    setReadIds(new Set())
    setMarks({})
    setCtxMenu(null)
    clearAllData()
  }, [clearAllData])

  const setFollow = useCallback((v: boolean) => setPref({ follow: v }), [setPref])
  const setSystemProxy = useCallback((v: boolean) => setPref({ systemProxy: v }), [setPref])
  const setAutoSystemProxy = useCallback((v: boolean) => setPref({ autoSystemProxy: v }), [setPref])
  const setThrottle = useCallback((v: boolean) => setPref({ throttle: v }), [setPref])

  const toggleSearch = useCallback(() => setPref({ searchVisible: !searchVisible }), [setPref, searchVisible])

  const focusSearch = useCallback(() => {
    setPref({ searchVisible: true })
    requestAnimationFrame(() => {
      searchRef.current?.focus()
      searchRef.current?.select()
    })
  }, [setPref])

  const clearFilter = useCallback(() => {
    setChip('all')
    setSearch('')
  }, [])

  const doExportHar = useCallback(() => exportHar(filteredRef.current), [])
  const doExportJson = useCallback(() => exportJson(filteredRef.current), [])
  const exportCaCert = useCallback(() => {
    Bridge.getCertificatePEM()
      .then((pem) => {
        if (pem) void saveFile(pem, 'sniffy-ca.crt')
      })
      .catch(() => {})
  }, [])

  /* ── 深链：?select=json|<idx> 自动选中一行 ── */
  useEffect(() => {
    const sel = new URLSearchParams(window.location.search).get('select')
    if (sel) {
      const target =
        sel === 'json'
          ? [...rows].reverse().find((r) => r.contentKind === 'json' && r.resBody) ?? rows[rows.length - 1]
          : rows[Number(sel) || 0]
      if (target) selectSingle(target.id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /* ── 子窗口 → 主窗口 的跨窗事件：导航 / 清空 ── */
  useEffect(() => {
    const offs: Array<() => void> = []
    try {
      offs.push(
        Events.On('main_nav', (e: { data?: string }) => {
          const v = e?.data
          if (typeof v === 'string' && NAV_VIEWS.includes(v as WorkbenchView)) setView(v as WorkbenchView)
        }),
      )
      offs.push(Events.On('data_cleared', () => clearLocal()))
    } catch {
      /* 非 Wails 环境：忽略 */
    }
    return () => {
      for (const off of offs) {
        try {
          off()
        } catch {
          /* ignore */
        }
      }
    }
  }, [clearLocal])

  /* ── 窗口缩放：重新夹紧详情宽度，避免压垮流量表 ── */
  useEffect(() => {
    const onResize = () => setDetailWidth((w) => clampDetail(w))
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  /* ── 全局快捷键 ── */
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.ctrlKey || e.metaKey
      if (mod && !e.shiftKey && e.key.toLowerCase() === 'f') {
        e.preventDefault()
        focusSearch()
        return
      }
      if (mod && !e.shiftKey && e.key.toLowerCase() === 'j' && !isTypingTarget()) {
        e.preventDefault()
        toggleTheme()
        return
      }
      // Ctrl/Cmd+R：暂停/继续捕获（并阻止 WebView 默认刷新，避免丢失内存状态）
      if (mod && !e.shiftKey && e.key.toLowerCase() === 'r') {
        e.preventDefault()
        toggleCapture()
        return
      }
      // Ctrl/Cmd+E：导出 HAR
      if (mod && !e.shiftKey && e.key.toLowerCase() === 'e') {
        e.preventDefault()
        doExportHar()
        return
      }
      if (e.key === 'Escape') {
        // 优先关闭右键菜单；输入框聚焦时把 Esc 留给输入框自己（清空/失焦）
        if (ctxMenu) setCtxMenu(null)
        else if (isTypingTarget()) return
        else if (focusedId || selectedIds.size) clearSelection()
        return
      }

      // 以下快捷键仅在流量视图、且焦点不在输入框时生效
      if (view !== 'traffic' || isTypingTarget()) return

      if (mod && e.key.toLowerCase() === 'a') {
        e.preventDefault()
        selectAll()
      } else if (mod && e.key === 'Delete') {
        // 与「文件 → 清空流量 Ctrl+Del」一致
        e.preventDefault()
        clear()
      } else if (e.key === 'Delete' || e.key === 'Backspace') {
        e.preventDefault()
        deleteSelected()
      } else if (mod && e.shiftKey && e.key.toLowerCase() === 'c') {
        e.preventDefault()
        if (focusedRow) void copyText(buildCurl(focusedRow))
      } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        if (!filtered.length) return
        const idx = filtered.findIndex((r) => r.id === focusedId)
        const next =
          idx < 0
            ? e.key === 'ArrowDown'
              ? filtered[0]
              : filtered[filtered.length - 1]
            : filtered[Math.min(filtered.length - 1, Math.max(0, idx + (e.key === 'ArrowDown' ? 1 : -1)))]
        if (next) selectSingle(next.id)
      } else if (e.altKey && MARK_BY_CODE[e.code]) {
        e.preventDefault()
        setMarkFor(MARK_BY_CODE[e.code])
      } else if (e.altKey && e.code === 'Digit0') {
        e.preventDefault()
        setMarkFor(undefined)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [
    focusSearch,
    toggleTheme,
    toggleCapture,
    doExportHar,
    ctxMenu,
    focusedId,
    focusedRow,
    selectedIds,
    clearSelection,
    selectAll,
    deleteSelected,
    clear,
    selectSingle,
    setMarkFor,
    filtered,
    view,
  ])

  /* ── 详情面板宽度拖拽 ── */
  const startResize = useCallback(
    (e: ReactPointerEvent) => {
      e.preventDefault()
      const startX = e.clientX
      const startW = detailWidth
      let lastW = startW
      const onMove = (ev: PointerEvent) => {
        lastW = clampDetail(startW + (startX - ev.clientX))
        setDetailWidth(lastW)
      }
      const onUp = () => {
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
        // 拖拽结束写回偏好（持久化）——副作用放在更新器之外，避免 StrictMode 双调用
        setPref({ detailWidth: lastW })
      }
      document.body.style.cursor = 'col-resize'
      document.body.style.userSelect = 'none'
      window.addEventListener('pointermove', onMove)
      window.addEventListener('pointerup', onUp)
    },
    [detailWidth, setPref],
  )

  /* ── 右键菜单 ── */
  const handleRowContextMenu = useCallback(
    (row: TrafficRow, e: ReactMouseEvent) => {
      e.preventDefault()
      // 右键未选中行：改为单选该行；右键已选行：保持多选，仅移动焦点
      if (!selectedIds.has(row.id)) {
        selectSingle(row.id)
      } else {
        setFocusedId(row.id)
        anchorRef.current = row.id
      }
      setCtxMenu({ x: e.clientX, y: e.clientY, rowId: row.id })
    },
    [selectedIds, selectSingle],
  )

  const ctxItems = useMemo<MenuNode[]>(() => {
    if (!ctxMenu) return []
    const row = rows.find((r) => r.id === ctxMenu.rowId)
    if (!row) return []
    const ids = selectedIds.size ? [...selectedIds] : [row.id]
    const many = ids.length > 1
    // 复制类操作的目标行集（与 copyFromRows 的取行逻辑一致），disabled 判据基于整个目标集
    const targets = selectedRows.length > 0 ? selectedRows : [row]
    // 右键行刚获焦点、auto-read effect 尚未提交，视为已读，避免菜单标签首帧跳变
    const allRead = ids.every((id) => readIds.has(id) || id === row.id)
    const anyMarked = ids.some((id) => marks[id])
    const curMark = marks[row.id]
    return [
      { label: t('workbench.ctx.copyCurl'), shortcut: 'Ctrl+Shift+C', icon: Terminal, onSelect: () => void copyText(buildCurl(row)) },
      {
        label: t('workbench.ctx.copy'),
        icon: Copy,
        submenu: [
          { label: many ? t('workbench.ctx.copyUrlN', { count: ids.length }) : 'URL', onSelect: () => copyFromRows((r) => r.url) },
          { label: 'Host', onSelect: () => copyFromRows((r) => r.host) },
          { label: t('workbench.ctx.copyPath'), onSelect: () => copyFromRows((r) => r.path) },
          { type: 'separator' },
          {
            label: t('workbench.ctx.copyReqHeaders'),
            disabled: !targets.some((r) => r.reqHeaders),
            onSelect: () => copyFromRows((r) => (r.reqHeaders ? headersToText(r.reqHeaders) : undefined)),
          },
          {
            label: t('workbench.ctx.copyResHeaders'),
            disabled: !targets.some((r) => r.resHeaders),
            onSelect: () => copyFromRows((r) => (r.resHeaders ? headersToText(r.resHeaders) : undefined)),
          },
          {
            label: t('workbench.ctx.copyReqBody'),
            disabled: !targets.some((r) => r.reqBody),
            onSelect: () => copyFromRows((r) => r.reqBody),
          },
          {
            label: t('workbench.ctx.copyResBody'),
            disabled: !targets.some((r) => r.resBody),
            onSelect: () => copyFromRows((r) => r.resBody),
          },
        ],
      },
      {
        label: t('workbench.ctx.select'),
        icon: ListChecks,
        submenu: [
          { label: t('workbench.ctx.selectAll'), shortcut: 'Ctrl+A', onSelect: selectAll },
          { label: t('workbench.ctx.deselect'), shortcut: 'Esc', onSelect: clearSelection },
          { label: t('workbench.ctx.invertSelection'), onSelect: invertSelection },
        ],
      },
      { type: 'separator' },
      {
        label: many ? t('workbench.ctx.resendN', { count: ids.length }) : t('workbench.ctx.resend'),
        icon: Send,
        onSelect: () => ids.forEach((id) => void Bridge.resendFlow(id).catch(() => {})),
      },
      { type: 'separator' },
      {
        label: t('workbench.ctx.highlight'),
        icon: Highlighter,
        submenu: [
          ...MARK_OPTIONS.map((m) => ({
            label: t(m.labelKey),
            swatch: m.swatch,
            shortcut: m.shortcut,
            checked: curMark === m.color,
            onSelect: () => setMarkFor(m.color),
          })),
          { type: 'separator' as const },
          { label: t('workbench.ctx.markReset'), shortcut: 'Alt+0', disabled: !anyMarked, onSelect: () => setMarkFor(undefined) },
        ],
      },
      allRead
        ? { label: many ? t('workbench.ctx.markUnreadN', { count: ids.length }) : t('workbench.ctx.markUnread'), icon: EyeOff, onSelect: () => setReadFor(false) }
        : { label: many ? t('workbench.ctx.markReadN', { count: ids.length }) : t('workbench.ctx.markRead'), icon: Eye, onSelect: () => setReadFor(true) },
      { type: 'separator' },
      {
        label: many ? t('workbench.ctx.deleteN', { count: ids.length }) : t('workbench.ctx.delete'),
        shortcut: 'Del',
        icon: Trash2,
        danger: true,
        onSelect: deleteSelected,
      },
      { label: t('workbench.ctx.clearAll'), icon: Trash2, danger: true, onSelect: clear },
    ]
  }, [
    ctxMenu,
    rows,
    selectedIds,
    selectedRows,
    readIds,
    marks,
    copyFromRows,
    selectAll,
    clearSelection,
    invertSelection,
    setMarkFor,
    setReadFor,
    deleteSelected,
    clear,
    t,
  ])

  /* ── 菜单 ── */
  const menus: TopMenu[] = useMemo(
    () => [
      {
        id: 'file',
        label: t('workbench.menu.file'),
        items: [
          { label: t('workbench.menu.exportHar'), shortcut: 'Ctrl+E', icon: FileDown, onSelect: doExportHar },
          { label: t('workbench.menu.exportJson'), icon: FileJson, onSelect: doExportJson },
          { type: 'separator' },
          { label: t('workbench.menu.clearTraffic'), shortcut: 'Ctrl+Del', icon: Trash2, danger: true, onSelect: clear },
        ],
      },
      {
        id: 'edit',
        label: t('workbench.menu.edit'),
        items: [
          { label: t('workbench.menu.find'), shortcut: 'Ctrl+F', onSelect: focusSearch },
          { label: t('workbench.menu.clearFilter'), onSelect: clearFilter },
          { type: 'separator' },
          { label: t('workbench.menu.selectAll'), shortcut: 'Ctrl+A', onSelect: selectAll },
          { label: t('workbench.menu.deselect'), shortcut: 'Esc', onSelect: clearSelection },
          { label: t('workbench.menu.invertSelection'), onSelect: invertSelection },
          { type: 'separator' },
          { label: t('workbench.menu.deleteSelected'), shortcut: 'Del', icon: Trash2, danger: true, onSelect: deleteSelected },
        ],
      },
      {
        id: 'view',
        label: t('workbench.menu.view'),
        items: [
          { label: isDark ? t('workbench.menu.switchToLight') : t('workbench.menu.switchToDark'), shortcut: 'Ctrl+J', onSelect: toggleTheme },
          { label: t('workbench.menu.followLatest'), checked: follow, onSelect: () => setFollow(!follow) },
          { label: searchVisible ? t('workbench.menu.hideSearch') : t('workbench.menu.showSearch'), onSelect: toggleSearch },
          { type: 'separator' },
          { label: t('workbench.menu.traffic'), checked: view === 'traffic', onSelect: () => setView('traffic') },
          { label: t('workbench.menu.rules'), checked: view === 'rules', onSelect: () => setView('rules') },
          { label: t('workbench.menu.breakpoints'), checked: view === 'breakpoints', onSelect: () => setView('breakpoints') },
          { label: t('workbench.menu.plugins'), onSelect: openPlugins },
          { label: t('workbench.menu.certs'), checked: view === 'certs', onSelect: () => setView('certs') },
          { label: t('workbench.menu.settings'), onSelect: openSettings },
        ],
      },
      {
        id: 'proxy',
        label: t('workbench.menu.proxy'),
        items: [
          { label: capturing ? t('workbench.menu.pauseCapture') : t('workbench.menu.resumeCapture'), shortcut: 'Ctrl+R', onSelect: toggleCapture },
          { type: 'separator' },
          { label: t('workbench.menu.systemProxy'), checked: systemProxy, onSelect: () => setSystemProxy(!systemProxy) },
          { label: t('workbench.menu.autoSystemProxy'), checked: autoSystemProxy, onSelect: () => setAutoSystemProxy(!autoSystemProxy) },
          { label: t('workbench.menu.throttle'), checked: throttle, onSelect: () => setThrottle(!throttle) },
          { label: t('workbench.menu.upstreamProxy'), onSelect: openSettings },
        ],
      },
      {
        id: 'tools',
        label: t('workbench.menu.tools'),
        items: [
          { label: t('workbench.menu.rules'), shortcut: 'Alt+K', icon: Shuffle, onSelect: () => setView('rules') },
          { label: t('workbench.menu.breakpoints'), shortcut: 'Alt+B', icon: CircleDot, onSelect: () => setView('breakpoints') },
          { label: t('workbench.menu.scriptsPlugins'), shortcut: 'Alt+P', icon: Puzzle, onSelect: openPlugins },
          { label: t('workbench.menu.throttle'), shortcut: 'Alt+J', icon: Gauge, checked: throttle, onSelect: () => setThrottle(!throttle) },
          { label: t('workbench.menu.proxyTerminal'), icon: Terminal, disabled: true },
          { type: 'separator' },
          {
            label: t('workbench.menu.decode'),
            icon: Code2,
            submenu: [
              { label: t('workbench.menu.base64Decode'), onSelect: () => void openToolboxWindow('base64dec').catch(() => {}) },
              { label: t('workbench.menu.urlDecode'), onSelect: () => void openToolboxWindow('urldec').catch(() => {}) },
              { label: t('workbench.menu.jwtDecode'), onSelect: () => void openToolboxWindow('jwt').catch(() => {}) },
            ],
          },
          {
            label: t('workbench.menu.encode'),
            icon: Braces,
            submenu: [
              { label: t('workbench.menu.base64Encode'), onSelect: () => void openToolboxWindow('base64enc').catch(() => {}) },
              { label: t('workbench.menu.urlEncode'), onSelect: () => void openToolboxWindow('urlenc').catch(() => {}) },
            ],
          },
          {
            label: t('workbench.menu.digest'),
            icon: Fingerprint,
            submenu: [
              { label: 'MD5', onSelect: () => void openToolboxWindow('md5').catch(() => {}) },
              { label: 'SHA-1', onSelect: () => void openToolboxWindow('sha1').catch(() => {}) },
              { label: 'SHA-256', onSelect: () => void openToolboxWindow('sha256').catch(() => {}) },
            ],
          },
          {
            label: t('workbench.menu.generate'),
            icon: Wand2,
            submenu: [
              { label: t('workbench.menu.timestamp'), onSelect: () => void openToolboxWindow('timestamp').catch(() => {}) },
              { label: 'UUID', onSelect: () => void openToolboxWindow('uuid').catch(() => {}) },
              { label: t('workbench.menu.qrCode'), icon: QrCode, onSelect: () => void openToolboxWindow('qr').catch(() => {}) },
            ],
          },
        ],
      },
      {
        id: 'certs',
        label: t('workbench.menu.certs'),
        items: [
          { label: t('workbench.menu.certManager'), icon: ShieldCheck, onSelect: () => setView('certs') },
          { label: t('workbench.menu.exportCert'), icon: Download, onSelect: exportCaCert },
          { label: t('workbench.menu.viewKey'), icon: KeyRound, disabled: true },
          { type: 'separator' },
          { label: t('workbench.menu.regenerateCa'), icon: RefreshCw, danger: true, disabled: true },
        ],
      },
      {
        id: 'help',
        label: t('workbench.menu.help'),
        items: [
          { label: t('workbench.menu.docs'), icon: Info, onSelect: () => openExternal(DOCS_URL) },
          { label: t('workbench.menu.about'), icon: Binary, onSelect: () => void openAboutWindow().catch(() => {}) },
        ],
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [
      t,
      isDark,
      follow,
      searchVisible,
      view,
      capturing,
      systemProxy,
      autoSystemProxy,
      throttle,
      clear,
      focusSearch,
      clearFilter,
      toggleTheme,
      toggleCapture,
      toggleSearch,
      openSettings,
      openPlugins,
      doExportHar,
      doExportJson,
      exportCaCert,
      selectAll,
      clearSelection,
      invertSelection,
      deleteSelected,
      setFollow,
      setSystemProxy,
      setAutoSystemProxy,
      setThrottle,
    ],
  )

  // macOS：菜单搬到顶部系统菜单栏（不在窗口内自绘）；其它平台仍由下方 TitleBar 自绘。
  useNativeMenu(menus, { openSettings, openAbout: () => void openAboutWindow().catch(() => {}) })

  return (
    <div className="wb-root flex h-screen w-screen flex-col overflow-hidden">
      {/* 各平台都渲染 TitleBar，按平台变体：mac 是托住红绿灯的拖拽条（无菜单），见 TitleBar 内分流。 */}
      <TitleBar menus={menus} isDark={isDark} onToggleTheme={toggleTheme} connected={isConnected} />

      <div className="flex min-h-0 flex-1">
        <IconRail view={view} onChange={handleNav} />

        <div className="flex min-h-0 flex-1 flex-col">
          {view === 'traffic' ? (
            <>
              <ProxyBar
                proxyAddr={proxyAddr}
                capturing={capturing}
                onToggleCapture={toggleCapture}
                onClear={clear}
                onNav={handleNav}
                onEditProxy={openSettings}
                systemProxy={systemProxy}
                onToggleSystemProxy={() => setSystemProxy(!systemProxy)}
                throttle={throttle}
                onToggleThrottle={() => setThrottle(!throttle)}
              />
              <Toolbar
                chips={chips}
                activeChip={chip}
                onChip={(k) => setChip(k as ChipKey)}
                search={search}
                onSearch={setSearch}
                follow={follow}
                onToggleFollow={() => setFollow(!follow)}
                searchVisible={searchVisible}
                onToggleSearch={toggleSearch}
                filterActive={chip !== 'all' || search.trim() !== ''}
                onClearFilter={clearFilter}
                searchRef={searchRef}
              />

              <div className="flex min-h-0 flex-1">
                <TrafficTable
                  rows={filtered}
                  focusedId={focusedId}
                  selectedIds={selectedIds}
                  readIds={readIds}
                  marks={marks}
                  onRowClick={handleRowClick}
                  onRowContextMenu={handleRowContextMenu}
                  onMarqueeSelect={handleMarqueeSelect}
                  onMarqueeEnd={handleMarqueeEnd}
                  follow={follow}
                />

                {focusedRow && (
                  <>
                    <div
                      onPointerDown={startResize}
                      className="group/divider w-px shrink-0 cursor-col-resize bg-line transition-colors hover:bg-accent"
                    >
                      <div className="h-full w-1 -translate-x-px" />
                    </div>
                    <div className="shrink-0" style={{ width: detailWidth }}>
                      {/* key 按行 id：切换行时重置子页签/查找态等内部状态，避免串台（Body 模式/分栏已提升到偏好层，不受影响） */}
                      <FindScope key={focusedRow.id}>
                        {focusedRow.kind === 'ws' ? (
                          <WsDetailPanel row={focusedRow} onClose={clearSelection} />
                        ) : focusedHasStream ? (
                          <StreamDetailPanel row={focusedRow} onClose={clearSelection} />
                        ) : (
                          <DetailPanel row={focusedRow} onClose={clearSelection} />
                        )}
                      </FindScope>
                    </div>
                  </>
                )}
              </div>

              {ctxMenu && ctxItems.length > 0 && (
                <ContextMenu x={ctxMenu.x} y={ctxMenu.y} items={ctxItems} onClose={() => setCtxMenu(null)} />
              )}
            </>
          ) : view === 'rules' ? (
            <RulesView />
          ) : view === 'breakpoints' ? (
            <BreakpointsView />
          ) : view === 'plugins' ? (
            <PluginsView />
          ) : view === 'certs' ? (
            <CertsView />
          ) : (
            <SettingsView />
          )}
        </div>
      </div>

      <StatusBar
        proxyAddr={proxyAddr}
        capturing={capturing}
        total={rows.length}
        filtered={filtered.length}
        selectedSeq={focusedRow?.seq}
        selectedCount={selectedIds.size}
        connected={isConnected}
      />
    </div>
  )
}
