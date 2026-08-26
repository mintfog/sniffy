/**
 * 断点处置的共用动作。放行 / 阻断 / 续期在三处入口都要用（断点页的列表行、
 * 流量表的右键菜单与详情横幅、编辑器的底部按钮），逻辑只能有一份——
 * 三处各写一遍的话，"后端说这条已不在暂停中"该怎么处理迟早会各说各话。
 */
import { useCallback, useMemo } from 'react'
import { Bridge } from '@/lib/bridge'
import { useAppStore } from '@/store'
import type { ResumePatch } from './model'

export interface BreakpointActions {
  /**
   * 放行；patch 为 null 表示原样放行。
   * 返回 false 表示后端说这条已不在暂停中（超时自动放行，或被另一个窗口处置过），
   * 调用方据此告诉用户「你的修改没有生效」而不是当作成功。
   * 校验失败会抛出，此时 flow 仍被按在断点上。
   */
  resume: (id: string, patch: ResumePatch | null) => Promise<boolean>
  abort: (id: string) => Promise<boolean>
  extend: (id: string) => void
}

export function useBreakpointActions(): BreakpointActions {
  const resolveOne = useCallback(async (id: string, run: () => Promise<boolean>) => {
    const stillPaused = await run()
    // 无论后端怎么说，这一条都已经离开断点，本地列表必须跟着摘掉。
    useAppStore.getState().removePausedFlow(id)
    if (!stillPaused) useAppStore.getState().noteResolved(id, 'resolved')
    return stillPaused
  }, [])

  const resume = useCallback(
    (id: string, patch: ResumePatch | null) => resolveOne(id, () => Bridge.resumeBreakpoint(id, patch)),
    [resolveOne],
  )
  const abort = useCallback((id: string) => resolveOne(id, () => Bridge.abortBreakpoint(id)), [resolveOne])
  // 新的截止时刻随 breakpoint_hit 回来，这里不做乐观更新，免得与后端各说各话。
  const extend = useCallback((id: string) => {
    Bridge.extendBreakpoint(id).catch(() => {})
  }, [])

  // 返回值必须是稳定引用:调用方会把它放进 useMemo/useCallback 的依赖里,
  // 每次渲染换一个新对象等于让那些 memo 全部失效。
  return useMemo(() => ({ resume, abort, extend }), [resume, abort, extend])
}
