import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { useAppStore } from '@/store'
import { usePrefs } from '../prefs'
import { formatSize } from '../lib/format'
import type { TrafficRow } from '../lib/types'
import { ProcessAvatar } from '../ui/primitives'
import { UrlHighlight } from './BodyViewer'
import { ActionIcon, Pill } from './messages/Bits'
import { WsMessagesPane } from './messages/WsMessagesPane'

export function WsDetailPanel({ row, onClose }: { row: TrafficRow; onClose: () => void }) {
  const { t } = useTranslation()
  // 直接订阅 store 中的完整 WS 会话（含 messages），新帧到达即实时刷新。
  const session = useAppStore((s) => s.webSocketSessions.find((x) => x.id === row.id))
  const frac = usePrefs((s) => s.detailTopFrac)
  const setPref = usePrefs((s) => s.set)

  const open = session?.status === 'open'
  const url = session?.url || row.url

  return (
    <div className="flex h-full min-h-0 flex-col border-l border-line bg-base">
      {/* 头部：URL + 类型/连接状态 + 关闭 */}
      <div className="flex shrink-0 items-start gap-2 border-b border-line bg-surface px-3 py-2.5">
        <div className="min-w-0 flex-1">
          <UrlHighlight url={url} />
          <div className="mt-1 flex items-center gap-2 text-2xs text-fg-faint">
            {session?.processName && (
              <span className="flex items-center gap-1">
                <ProcessAvatar name={session.processName} iconData={session.iconData} iconType={session.iconType} />
                <span className="truncate">{session.processName}</span>
              </span>
            )}
            <span className="tabular-nums">{t('detail.ws.messages')}: {session?.messageCount ?? 0}</span>
            <span className="tabular-nums">{formatSize(session?.totalSize)}</span>
          </div>
        </div>
        <Pill tone="info">WS</Pill>
        <Pill tone={open ? 'info' : 'neutral'}>{open ? t('detail.ws.statusOpen') : t('detail.ws.statusClosed')}</Pill>
        <ActionIcon title={t('detail.req.close')} onClick={onClose}>
          <X className="h-3.5 w-3.5" />
        </ActionIcon>
      </div>

      <WsMessagesPane session={session} topFrac={frac} onTopFracChange={(f) => setPref({ detailTopFrac: f })} />
    </div>
  )
}
