import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { useAppStore } from '@/store'
import { usePrefs } from '../prefs'
import { formatSize } from '../lib/format'
import type { TrafficRow } from '../lib/types'
import { ProcessAvatar } from '../ui/primitives'
import { UrlHighlight } from './BodyViewer'
import { RequestPane, TabRow } from './DetailPanel'
import { ActionIcon, Pill } from './messages/Bits'
import { streamKindLabel } from './messages/labels'
import { StreamMessagesPane } from './messages/StreamMessagesPane'

export function StreamDetailPanel({ row, onClose }: { row: TrafficRow; onClose: () => void }) {
  const { t } = useTranslation()
  // 直接订阅 store 中的完整流会话（含 messages），新消息到达即实时刷新。
  const session = useAppStore((s) => s.streamSessions.find((x) => x.id === row.id))
  const [view, setView] = useState<'messages' | 'request'>('messages')
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
        <Pill tone="info">{streamKindLabel[session?.kind ?? 'chunk'] || 'Stream'}</Pill>
        <Pill tone={open ? 'info' : 'neutral'}>{open ? t('detail.ws.statusOpen') : t('detail.ws.statusClosed')}</Pill>
        <ActionIcon title={t('detail.req.close')} onClick={onClose}>
          <X className="h-3.5 w-3.5" />
        </ActionIcon>
      </div>

      <TabRow
        tabs={[
          { key: 'messages', label: t('detail.stream.messages'), count: session?.messageCount ?? 0 },
          { key: 'request', label: t('detail.stream.request') },
        ]}
        active={view}
        onChange={(key) => setView(key as 'messages' | 'request')}
      />

      {view === 'request' ? (
        <RequestPane row={row} onClose={onClose} showClose={false} />
      ) : (
        <StreamMessagesPane session={session} topFrac={frac} onTopFracChange={(f) => setPref({ detailTopFrac: f })} />
      )}
    </div>
  )
}
