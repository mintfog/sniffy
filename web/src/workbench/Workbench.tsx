import { type PointerEvent as ReactPointerEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Binary,
  Braces,
  CircleDot,
  Code2,
  Download,
  FileDown,
  FileJson,
  Fingerprint,
  Gauge,
  Info,
  KeyRound,
  Puzzle,
  QrCode,
  RefreshCw,
  ShieldCheck,
  Shuffle,
  Terminal,
  Trash2,
  Wand2,
} from 'lucide-react'
import { useAppStore, useSystemStatus } from '@/store'
import './theme/tokens.css'
import { useTheme } from './theme/useTheme'
import { useTraffic } from './data/useTraffic'
import type { TrafficRow } from './lib/types'
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
import type { TopMenu } from './ui/Menu'

const PROXY_ADDR = '127.0.0.1:8080'
const DETAIL_MIN = 380
const DETAIL_MAX = 980

type ChipKey = 'all' | 'https' | 'http' | 'ws' | 'json' | 'image' | 'err'

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
  const { isDark, mode, setMode, toggle: toggleTheme } = useTheme()
  const { rows, isDemo, live, setLive, clearDemo, seedDemo } = useTraffic()
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
  const [selectedId, setSelectedId] = useState<string>()
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

  const selectedRow = useMemo(() => rows.find((r) => r.id === selectedId), [rows, selectedId])

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
    if (isDemo) setLive((v) => !v)
    else setStoreRecording(!storeRecording)
  }, [isDemo, setLive, setStoreRecording, storeRecording])

  const clear = useCallback(() => {
    setSelectedId(undefined)
    if (isDemo) clearDemo()
    else clearAllData()
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
      if (target) setSelectedId(target.id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /* ── 全局快捷键 ── */
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.ctrlKey || e.metaKey
      if (mod && e.key.toLowerCase() === 'f') {
        e.preventDefault()
        focusSearch()
      } else if (mod && e.key.toLowerCase() === 'j') {
        e.preventDefault()
        toggleTheme()
      } else if (e.key === 'Escape' && selectedId) {
        setSelectedId(undefined)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [focusSearch, toggleTheme, selectedId])

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
    [isDark, follow, view, capturing, systemProxy, throttle, clear, focusSearch, toggleTheme, toggleCapture, seedDemo],
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
                <TrafficTable rows={filtered} selectedId={selectedId} onSelect={setSelectedId} follow={follow} />

                {selectedRow && (
                  <>
                    <div
                      onPointerDown={startResize}
                      className="group/divider w-px shrink-0 cursor-col-resize bg-line transition-colors hover:bg-accent"
                    >
                      <div className="h-full w-1 -translate-x-px" />
                    </div>
                    <div className="shrink-0" style={{ width: detailWidth }}>
                      <DetailPanel row={selectedRow} onClose={() => setSelectedId(undefined)} />
                    </div>
                  </>
                )}
              </div>
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
        selectedSeq={selectedRow?.seq}
        connected={isConnected}
        isDemo={isDemo}
      />
    </div>
  )
}
