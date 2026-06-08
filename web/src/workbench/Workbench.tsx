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
import { useAppStore, useSystemStatus } from '@/store'
import { Bridge } from '@/lib/bridge'
import './theme/tokens.css'
import { useTheme } from './theme/useTheme'
import { useTraffic } from './data/useTraffic'
import { useBackendSync } from './data/useBackendSync'
import type { MarkColor, TrafficRow } from './lib/types'
import { buildCurl, copyText, headersToText } from './lib/clipboard'
import { TitleBar } from './shell/TitleBar'
import { IconRail, type WorkbenchView } from './shell/IconRail'
import { ProxyBar } from './shell/ProxyBar'
import { Toolbar, type FilterChip } from './shell/Toolbar'
import { StatusBar } from './shell/StatusBar'
import { TrafficTable } from './views/TrafficTable'
import { DetailPanel } from './views/DetailPanel'
import { SettingsView } from './views/SettingsView'
import { RulesView } from './views/RulesView'
import { BreakpointsView } from './views/BreakpointsView'
import { PluginsView } from './views/PluginsView'
import { CertsView } from './views/CertsView'
import { ContextMenu, type MenuNode, type TopMenu } from './ui/Menu'

const PROXY_ADDR = '127.0.0.1:8080'
const DETAIL_MIN = 380
const DETAIL_MAX = 980

type ChipKey = 'all' | 'https' | 'http' | 'ws' | 'json' | 'image' | 'err'

/** 高亮标记的菜单选项（颜色 + 快捷键，参考竞品） */
const MARK_OPTIONS: { color: MarkColor; label: string; swatch: string; shortcut: string }[] = [
  { color: 'red', label: '红色', swatch: 'bg-rose-500', shortcut: 'Alt+1' },
  { color: 'yellow', label: '黄色', swatch: 'bg-amber-400', shortcut: 'Alt+2' },
  { color: 'green', label: '绿色', swatch: 'bg-emerald-500', shortcut: 'Alt+3' },
  { color: 'blue', label: '蓝色', swatch: 'bg-sky-500', shortcut: 'Alt+4' },
  { color: 'cyan', label: '青色', swatch: 'bg-cyan-400', shortcut: 'Alt+5' },
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

export default function Workbench() {
  useBackendSync() // 连接 Wails v3 后端：回填会话 + 订阅实时事件 + 录制状态
  const { isDark, mode, setMode, toggle: toggleTheme } = useTheme()
  const { rows, isDemo, live, setLive, clearDemo, seedDemo, removeRows } = useTraffic()
  const { isConnected } = useSystemStatus()
  const storeRecording = useAppStore((s) => s.isRecording)
  const setStoreRecording = useAppStore((s) => s.setRecording)
  const clearAllData = useAppStore((s) => s.clearAllData)

  const [view, setView] = useState<WorkbenchView>(() => {
    const v = new URLSearchParams(window.location.search).get('view')
    return (['traffic', 'rules', 'breakpoints', 'plugins', 'certs', 'settings'].includes(v ?? '')
      ? v
      : 'traffic') as WorkbenchView
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
  const [follow, setFollow] = useState(true)
  const [detailWidth, setDetailWidth] = useState(480)
  const [systemProxy, setSystemProxy] = useState(true)
  const [throttle, setThrottle] = useState(false)

  const searchRef = useRef<HTMLInputElement>(null)
  const capturing = isDemo ? live : storeRecording

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
  const selectedRows = useMemo(() => rows.filter((r) => selectedIds.has(r.id)), [rows, selectedIds])

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

  const selectAll = useCallback(() => {
    setSelectedIds(new Set(filtered.map((r) => r.id)))
  }, [filtered])

  const invertSelection = useCallback(() => {
    const next = new Set(filtered.filter((r) => !selectedIds.has(r.id)).map((r) => r.id))
    setSelectedIds(next)
    // 焦点行/锚点被反选掉时同步清掉，避免详情面板展示未选中行、Shift 范围从陈旧锚点起算
    setFocusedId((f) => (f && next.has(f) ? f : undefined))
    if (anchorRef.current && !next.has(anchorRef.current)) anchorRef.current = undefined
  }, [filtered, selectedIds])

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

  // 已阅/标记按「存活行」回收（行被删除或 demo 裁剪后清理，防止无界增长）；
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
  /** 批量操作的目标：多选集合，否则焦点行 */
  const targetIds = useCallback((): string[] => {
    if (selectedIds.size > 0) return [...selectedIds]
    return focusedId ? [focusedId] : []
  }, [selectedIds, focusedId])

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
    removeRows(ids)
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
  }, [targetIds, removeRows])

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
    { key: 'all', label: '全部', count: counts.all },
    { key: 'https', label: 'HTTPS', count: counts.https },
    { key: 'http', label: 'HTTP', count: counts.http },
    { key: 'ws', label: 'WS', count: counts.ws },
    { key: 'json', label: 'JSON', count: counts.json },
    { key: 'image', label: '图片', count: counts.image },
    { key: 'err', label: '错误', count: counts.err },
  ]

  /* ── 动作 ── */
  // 暂停/继续「捕获」：端口始终监听，这里只控制是否把新流量记入表格。
  const toggleCapture = useCallback(() => {
    if (isDemo) {
      setLive((v) => !v)
      return
    }
    const next = !storeRecording
    setStoreRecording(next) // 乐观更新；后端无录制状态推送
    void (next ? Bridge.startRecording() : Bridge.stopRecording()).catch(() => {})
  }, [isDemo, setLive, setStoreRecording, storeRecording])

  const clear = useCallback(() => {
    setSelectedIds(new Set())
    setFocusedId(undefined)
    anchorRef.current = undefined
    setReadIds(new Set())
    setMarks({})
    setCtxMenu(null)
    if (isDemo) {
      clearDemo()
    } else {
      clearAllData()
      void Bridge.clearSessions().catch(() => {})
    }
  }, [isDemo, clearDemo, clearAllData])

  const focusSearch = useCallback(() => {
    searchRef.current?.focus()
    searchRef.current?.select()
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

  /* ── 全局快捷键 ── */
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.ctrlKey || e.metaKey
      if (mod && !e.shiftKey && e.key.toLowerCase() === 'f') {
        e.preventDefault()
        focusSearch()
        return
      }
      if (mod && e.key.toLowerCase() === 'j' && !isTypingTarget()) {
        e.preventDefault()
        toggleTheme()
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
      const onMove = (ev: PointerEvent) => {
        setDetailWidth(Math.min(DETAIL_MAX, Math.max(DETAIL_MIN, startW + (startX - ev.clientX))))
      }
      const onUp = () => {
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
      }
      document.body.style.cursor = 'col-resize'
      document.body.style.userSelect = 'none'
      window.addEventListener('pointermove', onMove)
      window.addEventListener('pointerup', onUp)
    },
    [detailWidth],
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
      { label: '复制 cURL', shortcut: 'Ctrl+Shift+C', icon: Terminal, onSelect: () => void copyText(buildCurl(row)) },
      {
        label: '复制',
        icon: Copy,
        submenu: [
          { label: many ? `URL（${ids.length} 条）` : 'URL', onSelect: () => copyFromRows((r) => r.url) },
          { label: 'Host', onSelect: () => copyFromRows((r) => r.host) },
          { label: '路径', onSelect: () => copyFromRows((r) => r.path) },
          { type: 'separator' },
          {
            label: '请求头',
            disabled: !targets.some((r) => r.reqHeaders),
            onSelect: () => copyFromRows((r) => (r.reqHeaders ? headersToText(r.reqHeaders) : undefined)),
          },
          {
            label: '响应头',
            disabled: !targets.some((r) => r.resHeaders),
            onSelect: () => copyFromRows((r) => (r.resHeaders ? headersToText(r.resHeaders) : undefined)),
          },
          {
            label: '请求体',
            disabled: !targets.some((r) => r.reqBody),
            onSelect: () => copyFromRows((r) => r.reqBody),
          },
          {
            label: '响应体',
            disabled: !targets.some((r) => r.resBody),
            onSelect: () => copyFromRows((r) => r.resBody),
          },
        ],
      },
      {
        label: '选择',
        icon: ListChecks,
        submenu: [
          { label: '全选', shortcut: 'Ctrl+A', onSelect: selectAll },
          { label: '取消选择', shortcut: 'Esc', onSelect: clearSelection },
          { label: '反选', onSelect: invertSelection },
        ],
      },
      { type: 'separator' },
      { label: '重发', icon: Send, disabled: true },
      { type: 'separator' },
      {
        label: '高亮',
        icon: Highlighter,
        submenu: [
          ...MARK_OPTIONS.map((m) => ({
            label: m.label,
            swatch: m.swatch,
            shortcut: m.shortcut,
            checked: curMark === m.color,
            onSelect: () => setMarkFor(m.color),
          })),
          { type: 'separator' as const },
          { label: '重置', shortcut: 'Alt+0', disabled: !anyMarked, onSelect: () => setMarkFor(undefined) },
        ],
      },
      allRead
        ? { label: many ? `标记 ${ids.length} 项未读` : '标记未读', icon: EyeOff, onSelect: () => setReadFor(false) }
        : { label: many ? `标记 ${ids.length} 项已阅` : '标记已阅', icon: Eye, onSelect: () => setReadFor(true) },
      { type: 'separator' },
      {
        label: many ? `删除 ${ids.length} 项` : '删除',
        shortcut: 'Del',
        icon: Trash2,
        danger: true,
        onSelect: deleteSelected,
      },
      { label: '清空全部', icon: Trash2, danger: true, onSelect: clear },
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
  ])

  /* ── 菜单 ── */
  const menus: TopMenu[] = useMemo(
    () => [
      {
        label: '文件',
        items: [
          { label: '导出 HAR…', shortcut: 'Ctrl+E', icon: FileDown, onSelect: () => {} },
          { label: '导出 JSON…', icon: FileJson, onSelect: () => {} },
          { type: 'separator' },
          { label: '清空流量', shortcut: 'Ctrl+Del', icon: Trash2, danger: true, onSelect: clear },
        ],
      },
      {
        label: '编辑',
        items: [
          { label: '查找', shortcut: 'Ctrl+F', onSelect: focusSearch },
          { label: '清除筛选', onSelect: () => { setChip('all'); setSearch('') } },
          { type: 'separator' },
          { label: '全选', shortcut: 'Ctrl+A', onSelect: selectAll },
          { label: '取消选择', shortcut: 'Esc', onSelect: clearSelection },
          { label: '反选', onSelect: invertSelection },
          { type: 'separator' },
          { label: '删除选中', shortcut: 'Del', icon: Trash2, danger: true, onSelect: deleteSelected },
        ],
      },
      {
        label: '视图',
        items: [
          { label: isDark ? '切换到亮色主题' : '切换到深色主题', shortcut: 'Ctrl+J', onSelect: toggleTheme },
          { label: '跟随最新', checked: follow, onSelect: () => setFollow((v) => !v) },
          { type: 'separator' },
          { label: '流量', checked: view === 'traffic', onSelect: () => setView('traffic') },
          { label: '重写规则', checked: view === 'rules', onSelect: () => setView('rules') },
          { label: '断点', checked: view === 'breakpoints', onSelect: () => setView('breakpoints') },
          { label: '插件', checked: view === 'plugins', onSelect: () => setView('plugins') },
          { label: '证书', checked: view === 'certs', onSelect: () => setView('certs') },
          { label: '设置', checked: view === 'settings', onSelect: () => setView('settings') },
        ],
      },
      {
        label: '代理',
        items: [
          { label: capturing ? '暂停捕获' : '继续捕获', shortcut: 'Ctrl+R', onSelect: toggleCapture },
          { type: 'separator' },
          { label: '系统代理', checked: systemProxy, onSelect: () => setSystemProxy((v) => !v) },
          { label: '网络限速', checked: throttle, onSelect: () => setThrottle((v) => !v) },
          { label: '上游代理…', onSelect: () => {} },
        ],
      },
      {
        label: '工具',
        items: [
          { label: '重写规则', shortcut: 'Alt+K', icon: Shuffle, onSelect: () => setView('rules') },
          { label: '断点', shortcut: 'Alt+B', icon: CircleDot, onSelect: () => setView('breakpoints') },
          { label: '脚本 / 插件', shortcut: 'Alt+P', icon: Puzzle, onSelect: () => setView('plugins') },
          { label: '网络限速', shortcut: 'Alt+J', icon: Gauge, checked: throttle, onSelect: () => setThrottle((v) => !v) },
          { label: '代理终端', shortcut: 'Alt+T', icon: Terminal, onSelect: () => {} },
          { type: 'separator' },
          {
            label: '解码',
            icon: Code2,
            submenu: [
              { label: 'Base64 解码', onSelect: () => {} },
              { label: 'URL 解码', onSelect: () => {} },
              { label: 'JWT 解析', onSelect: () => {} },
            ],
          },
          {
            label: '编码',
            icon: Braces,
            submenu: [
              { label: 'Base64 编码', onSelect: () => {} },
              { label: 'URL 编码', onSelect: () => {} },
            ],
          },
          {
            label: '消息摘要',
            icon: Fingerprint,
            submenu: [
              { label: 'MD5', onSelect: () => {} },
              { label: 'SHA-1', onSelect: () => {} },
              { label: 'SHA-256', onSelect: () => {} },
            ],
          },
          {
            label: '生成',
            icon: Wand2,
            submenu: [
              { label: '时间戳', onSelect: () => {} },
              { label: 'UUID', onSelect: () => {} },
              { label: '二维码', icon: QrCode, onSelect: () => {} },
            ],
          },
          { type: 'separator' },
          { label: '重新填充演示数据', icon: RefreshCw, onSelect: seedDemo },
        ],
      },
      {
        label: '证书',
        items: [
          { label: '证书管理', icon: ShieldCheck, onSelect: () => setView('certs') },
          { label: '导出证书…', icon: Download, onSelect: () => {} },
          { label: '查看密钥', icon: KeyRound, onSelect: () => {} },
          { type: 'separator' },
          { label: '重新生成 CA', icon: RefreshCw, danger: true, onSelect: () => {} },
        ],
      },
      {
        label: '帮助',
        items: [
          { label: '文档', icon: Info, onSelect: () => {} },
          { label: '关于 Sniffy', icon: Binary, onSelect: () => {} },
        ],
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [isDark, follow, view, capturing, systemProxy, throttle, clear, focusSearch, toggleTheme, toggleCapture, seedDemo, selectAll, clearSelection, invertSelection, deleteSelected],
  )

  return (
    <div className="wb-root flex h-screen w-screen flex-col overflow-hidden">
      <TitleBar menus={menus} isDark={isDark} onToggleTheme={toggleTheme} connected={isConnected} isDemo={isDemo} />

      <div className="flex min-h-0 flex-1">
        <IconRail view={view} onChange={setView} />

        <div className="flex min-h-0 flex-1 flex-col">
          {view === 'traffic' ? (
            <>
              <ProxyBar
                proxyAddr={PROXY_ADDR}
                capturing={capturing}
                isDemo={isDemo}
                onToggleCapture={toggleCapture}
                onClear={clear}
                onNav={setView}
                systemProxy={systemProxy}
                onToggleSystemProxy={() => setSystemProxy((v) => !v)}
                throttle={throttle}
                onToggleThrottle={() => setThrottle((v) => !v)}
              />
              <Toolbar
                chips={chips}
                activeChip={chip}
                onChip={(k) => setChip(k as ChipKey)}
                search={search}
                onSearch={setSearch}
                follow={follow}
                onToggleFollow={() => setFollow((v) => !v)}
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
                      {/* key 按行 id：切换行时重置子页签/Body 模式/JSON 折叠等内部状态，避免串台 */}
                      <DetailPanel key={focusedRow.id} row={focusedRow} onClose={clearSelection} />
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
            <SettingsView mode={mode} setMode={setMode} />
          )}
        </div>
      </div>

      <StatusBar
        proxyAddr={PROXY_ADDR}
        capturing={capturing}
        total={rows.length}
        filtered={filtered.length}
        selectedSeq={focusedRow?.seq}
        selectedCount={selectedIds.size}
        connected={isConnected}
        isDemo={isDemo}
      />
    </div>
  )
}
