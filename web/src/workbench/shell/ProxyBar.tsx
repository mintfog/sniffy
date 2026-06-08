import { CircleDot, Gauge, Globe, Pause, Pencil, Play, Puzzle, ShieldCheck, Shuffle, Trash2 } from 'lucide-react'
import type { WorkbenchView } from './IconRail'
import { cx, Divider, IconButton, Tooltip } from '../ui/primitives'

interface ProxyBarProps {
  proxyAddr: string
  capturing: boolean
  isDemo: boolean
  onToggleCapture: () => void
  onClear: () => void
  onNav: (v: WorkbenchView) => void
  onEditProxy: () => void
  systemProxy: boolean
  onToggleSystemProxy: () => void
  throttle: boolean
  onToggleThrottle: () => void
}

export function ProxyBar({
  proxyAddr,
  capturing,
  isDemo,
  onToggleCapture,
  onClear,
  onNav,
  onEditProxy,
  systemProxy,
  onToggleSystemProxy,
  throttle,
  onToggleThrottle,
}: ProxyBarProps) {
  return (
    <div className="flex h-11 items-center gap-2 border-b border-line bg-surface px-2">
      {/* 代理状态条：端口常驻监听（软件开着就一直监听） */}
      <div className="flex h-7 min-w-0 flex-1 items-center gap-2 rounded-wb border border-line bg-inset px-2.5">
        <span className="h-2 w-2 shrink-0 rounded-full bg-ok wb-pulse" />
        <span className="truncate text-[12.5px] text-fg">
          监听中 <span className="font-mono text-fg-muted">{proxyAddr}</span>
        </span>
        <span
          className={cx(
            'rounded-full px-1.5 py-px text-[10px] font-medium',
            systemProxy ? 'bg-ok/15 text-ok' : 'bg-fg-faint/15 text-fg-faint',
          )}
        >
          系统代理{systemProxy ? '已开' : '未开'}
        </span>
        {!capturing && <span className="rounded-full bg-warn/15 px-1.5 py-px text-[10px] font-medium text-warn">已暂停</span>}
        {isDemo && <span className="rounded-full bg-iris/15 px-1.5 py-px text-[10px] font-medium text-iris">演示数据</span>}
        <Tooltip label="编辑代理配置" placement="bottom">
          <button
            type="button"
            onClick={onEditProxy}
            aria-label="编辑代理配置"
            className="ml-auto flex h-5 w-5 items-center justify-center rounded-sm text-fg-faint hover:bg-elevated hover:text-fg"
          >
            <Pencil className="h-3 w-3" />
          </button>
        </Tooltip>
      </div>

      {/* 功能图标组 */}
      <div className="flex shrink-0 items-center gap-0.5">
        <Tooltip label={systemProxy ? '关闭系统代理' : '开启系统代理'} placement="bottom">
          <IconButton active={systemProxy} onClick={onToggleSystemProxy}>
            <Globe className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label="重写规则" placement="bottom">
          <IconButton onClick={() => onNav('rules')}>
            <Shuffle className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label="断点" placement="bottom">
          <IconButton onClick={() => onNav('breakpoints')}>
            <CircleDot className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label="脚本 / 插件" placement="bottom">
          <IconButton onClick={() => onNav('plugins')}>
            <Puzzle className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label="网络限速" placement="bottom">
          <IconButton active={throttle} onClick={onToggleThrottle}>
            <Gauge className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label="证书" placement="bottom">
          <IconButton onClick={() => onNav('certs')}>
            <ShieldCheck className="h-4 w-4" />
          </IconButton>
        </Tooltip>
      </div>

      <Divider vertical className="mx-0.5 my-2" />

      {/* 暂停 / 继续 捕获（端口不受影响，仅控制是否把新流量记入表格） */}
      <Tooltip label={capturing ? '暂停后端口仍监听，仅停止记录新流量' : '继续记录新流量'} placement="bottom">
        <button
          type="button"
          onClick={onToggleCapture}
          className={cx(
            'inline-flex h-7 shrink-0 items-center gap-1.5 rounded-wb px-3 text-[12px] font-semibold transition-colors outline-none',
            capturing ? 'bg-inset text-fg border border-line hover:bg-elevated' : 'bg-accent text-accent-fg hover:bg-accent-hover',
          )}
        >
          {capturing ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5 fill-current" />}
          {capturing ? '暂停' : '继续'}
        </button>
      </Tooltip>
      <Tooltip label="清空流量" placement="bottom">
        <IconButton onClick={onClear} tone="danger">
          <Trash2 className="h-4 w-4" />
        </IconButton>
      </Tooltip>
    </div>
  )
}
