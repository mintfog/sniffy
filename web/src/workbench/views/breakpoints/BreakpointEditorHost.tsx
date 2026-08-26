/**
 * 断点编辑器的唯一挂载点，挂在工作台根上。
 *
 * 编辑器是应用级模态：断点页的「编辑」、流量表的右键菜单与双击、详情面板的横幅
 * 都指向同一个入口（store 里的 editingBreakpointId）。各处各挂一个的话，
 * 同一条 flow 可能被两个编辑器同时打开，谁的修改算数就说不清了。
 */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppStore, useEditingBreakpointId, usePausedFlows, useResolutions } from '@/store'
import { BreakpointEditor } from './BreakpointEditor'
import { useBreakpointActions } from './actions'
import type { PausedFlow, ResumePatch } from './model'

export function BreakpointEditorHost({ onNotice }: { onNotice?: (text: string) => void }) {
  const { t } = useTranslation()
  const editingId = useEditingBreakpointId()
  const paused = usePausedFlows()
  const resolutions = useResolutions()
  const { resume, abort, extend } = useBreakpointActions()
  // 打开那一刻的快照。超时会把这条从列表里摘走，只认列表的话，编辑器会连同用户
  // 改到一半的内容一起凭空消失。
  const [snapshot, setSnapshot] = useState<PausedFlow | null>(null)
  // 每秒重算倒计时。
  const [now, setNow] = useState(() => Date.now())

  const live = editingId ? paused.find((p) => p.id === editingId) : undefined

  useEffect(() => {
    if (!editingId) {
      setSnapshot(null)
      return
    }
    setSnapshot((prev) => (prev?.id === editingId ? prev : (paused.find((p) => p.id === editingId) ?? null)))
  }, [editingId, paused])

  useEffect(() => {
    if (!editingId) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [editingId])

  const close = useCallback(() => useAppStore.getState().setEditingBreakpoint(undefined), [])

  const dispose = useCallback(
    async (run: () => Promise<boolean>) => {
      const stillPaused = await run()
      if (!stillPaused) onNotice?.(t('breakpoints.paused.stale'))
      close()
    },
    [close, onNotice, t],
  )

  const item = live ?? snapshot
  if (!editingId || !item) return null

  return (
    <BreakpointEditor
      item={item}
      remaining={live ? Math.max(0, Math.round((live.pausedUntil - now) / 1000)) : 0}
      goneReason={live ? undefined : resolutions[editingId] === 'expired' ? 'expired' : 'resolved'}
      onResume={(patch: ResumePatch | null) => dispose(() => resume(editingId, patch))}
      onAbort={() => dispose(() => abort(editingId))}
      onExtend={() => extend(editingId)}
      onClose={close}
    />
  )
}
