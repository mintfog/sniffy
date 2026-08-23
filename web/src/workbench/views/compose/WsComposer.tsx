/**
 * 出站 WebSocket 的发帧区，驻留在帧列表底部。
 *
 * 几何与线缆读数的底部坞同源（shrink-0 border-t bg-inset），两者是同一条「底边」。
 */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Send } from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { Button } from '../../ui/controls'
import { cx } from '../../ui/primitives'
import { wsOf, type Draft, type WebSocketPart, type WsPatch } from './model'

export function WsComposer({
  draft,
  conn,
  onPatchWs,
}: {
  draft: Draft
  conn: WebSocketPart['conn']
  /**
   * 必须是对 ws 分片做函数式合并的补丁器（ComposeView.patchWs），不能是整片替换的 patch：
   * send 会在 await 之后才清空输入框，而它闭包里的 part 是发送那一刻的旧快照，
   * 整片替换会把用户在等待期间键入的下一帧、以及文本/二进制开关一起回滚掉。
   */
  onPatchWs: (patch: WsPatch) => void
}) {
  const { t } = useTranslation()
  const [error, setError] = useState<string | undefined>(undefined)
  const part = wsOf(draft)
  const live = conn === 'open' && !!draft.sentFlowId
  const canSend = live && part.outgoing !== ''

  const edit = onPatchWs

  const hint = error
    ? t('compose.ws.sendFailed', { reason: error })
    : !live
      ? t('compose.ws.notConnected')
      : part.outgoingBinary
        ? t('compose.ws.binaryHint')
        : ''

  const send = async () => {
    if (!canSend || !draft.sentFlowId) return
    setError(undefined)
    const sent = part.outgoing
    try {
      // 二进制帧的载荷由用户按 base64 键入（见 compose.ws.binaryHint），与回程 DTO 对称。
      await Bridge.sendWSMessage(draft.sentFlowId, part.outgoingBinary ? 'binary' : 'text', sent)
      // 这次 await 可以长到秒级——Go 侧要过插件钩子（每插件 100ms 上限），对端不读时
      // 写还带 10s 期限。期间用户完全可能把下一帧敲进去，所以只清「还是刚发出去那份」的内容。
      edit((prev) => (prev.outgoing === sent ? { outgoing: '' } : {}))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="shrink-0 border-t border-line bg-inset">
      <div className="flex items-center gap-1.5 px-2 pt-1.5">
        <div className="flex overflow-hidden rounded-control border border-line">
          {([false, true] as const).map((bin) => (
            <button
              key={String(bin)}
              type="button"
              onClick={() => edit({ outgoingBinary: bin })}
              title={bin ? t('compose.ws.binaryHint') : undefined}
              className={cx(
                'px-2 py-[1px] font-mono text-2xs font-semibold transition',
                part.outgoingBinary === bin ? 'bg-elevated text-fg' : 'text-fg-faint hover:text-fg',
              )}
            >
              {bin ? t('compose.ws.binary') : t('compose.ws.text')}
            </button>
          ))}
        </div>
        <span className={cx('min-w-0 flex-1 truncate text-2xs', error ? 'text-danger' : 'text-fg-faint')}>{hint}</span>
        <Button
          variant="primary"
          size="sm"
          onClick={() => void send()}
          disabled={!canSend}
          title={t('compose.ws.send')}
          icon={<Send className="h-3 w-3" />}
          className="shrink-0"
        >
          {t('compose.ws.send')}
        </Button>
      </div>
      <textarea
        value={part.outgoing}
        spellCheck={false}
        disabled={!live}
        onChange={(e) => edit({ outgoing: e.target.value })}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
            // 不让它冒到窗口级的 Ctrl/⌘+Enter——那一个是「发请求 / 连接」。
            e.preventDefault()
            e.stopPropagation()
            void send()
          }
        }}
        placeholder={t('compose.ws.messagePlaceholder')}
        aria-label={t('compose.ws.send')}
        className="h-16 w-full resize-none bg-transparent px-3 py-1.5 font-mono text-[11.5px] leading-[1.55] text-fg outline-none placeholder:font-sans placeholder:text-fg-faint disabled:text-fg-faint"
      />
    </div>
  )
}
