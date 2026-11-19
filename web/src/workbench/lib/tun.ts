import { useEffect } from 'react'
import { create } from 'zustand'
import { Events } from '@wailsio/runtime'
import { Bridge, type TunStatus } from '@/lib/bridge'
import { pollSnapshot } from './snapshotSync'

export const TUN_TRANSIENT_SYNC_MS = 2000
export const TUN_STABLE_SYNC_MS = 30_000

/**
 * 界面上的阶段：在 TunStatus.state 之外，把关闭请求进行中归为 stopping（开启中被要求关闭时，
 * 后端要等放弃开启之后才改状态），把带原因的 stopping 单独列为 stuck：没能及时关掉，或开启失败
 * 而网卡可能还在，两种情形都要等网卡消失后才能再开启。
 */
export type TunPhase = 'off' | 'starting' | 'running' | 'stopping' | 'stuck' | 'failed'

/** 需要弹窗告知的出错原因；code 是后端给的分类，stuck 表示网卡可能还在。 */
interface TunNotice {
  error: string
  code?: TunStatus['errorCode']
  stuck: boolean
}

interface TunState {
  status: TunStatus
  /** StopTun 调用进行中。 */
  stopPending: boolean
  /** 根证书未被信任，等用户决定是否仍然开启。 */
  untrustedPrompt: boolean
  notice: TunNotice | null
}

/** 主窗口内共享的虚拟网卡状态，由 useTunSync 与后端保持同步。 */
export const useTun = create<TunState>(() => ({
  // 比后端任何快照都旧，第一份到达的快照必定被采纳。
  status: { revision: -1, state: 'off' },
  stopPending: false,
  untrustedPrompt: false,
  notice: null,
}))

export function tunPhaseOf(status: TunStatus, stopPending: boolean): TunPhase {
  if (status.state === 'stopping') return status.error ? 'stuck' : 'stopping'
  if (stopPending && (status.state === 'starting' || status.state === 'running')) return 'stopping'
  return status.state
}

function isTunActive(phase: TunPhase): boolean {
  return phase === 'starting' || phase === 'running'
}

export function isTunClosing(phase: TunPhase): boolean {
  return phase === 'stopping' || phase === 'stuck'
}

/** 开关显示为开：开启中、运行中与关闭进行中。 */
export function isTunOn(phase: TunPhase): boolean {
  return phase !== 'off' && phase !== 'failed'
}

/** 状态还会自行变化的阶段：开启中与关闭进行中。 */
export function isTunTransient(phase: TunPhase): boolean {
  return phase === 'starting' || isTunClosing(phase)
}

export function useTunPhase(): TunPhase {
  return useTun((s) => tunPhaseOf(s.status, s.stopPending))
}

function noticeOf(status: TunStatus): TunNotice | null {
  if (!status.error) return null
  return { error: status.error, code: status.errorCode, stuck: status.state === 'stopping' }
}

/** 新出现的出错原因要告知用户；窗口打开前就已存在的只在状态栏常驻显示。 */
function newTunNotice(prev: TunStatus, next: TunStatus): TunNotice | null {
  if (prev.revision < 0 || next.error === prev.error) return null
  return noticeOf(next)
}

/** 查询结果、调用返回值与 tun_state 推送先后不定，只采纳 revision 更高的快照。 */
export function acceptTunStatus(next: TunStatus) {
  useTun.setState((s) => {
    if (next.revision <= s.status.revision) return s
    return { status: next, notice: newTunNotice(s.status, next) ?? s.notice }
  })
}

function refreshTunStatus(): Promise<void> {
  return Bridge.getTunStatus()
    .then(acceptTunStatus)
    .catch(() => {})
}

/** 根证书未被系统信任（检查出错同样按未信任处理）时先征求用户意见，否则直接开启。 */
async function requestStartTun() {
  const trusted = await Bridge.isCATrusted().catch(() => false)
  if (trusted) startTun()
  else useTun.setState({ untrustedPrompt: true })
}

export function startTun() {
  useTun.setState({ untrustedPrompt: false })
  Bridge.startTun()
    .then(acceptTunStatus)
    .catch((e: unknown) => {
      useTun.setState({ notice: { error: e instanceof Error ? e.message : String(e), stuck: false } })
      void refreshTunStatus()
    })
}

/** 关不掉时原因随状态推送，这里只需把状态拉回来。failed 时 StopTun 清除失败原因、回到 off。 */
function stopTun() {
  useTun.setState({ stopPending: true })
  Bridge.stopTun()
    .then(acceptTunStatus)
    .catch(refreshTunStatus)
    .finally(() => useTun.setState({ stopPending: false }))
}

export function toggleTun() {
  const { status, stopPending } = useTun.getState()
  const phase = tunPhaseOf(status, stopPending)
  if (isTunActive(phase)) stopTun()
  else if (!isTunClosing(phase)) void requestStartTun()
}

/** 弹窗显示当前状态里的出错原因（状态栏的常驻入口用）。 */
export function showTunError() {
  useTun.setState({ notice: noticeOf(useTun.getState().status) })
}

export function closeUntrustedPrompt() {
  useTun.setState({ untrustedPrompt: false })
}

/** 关闭出错弹窗；状态为 failed 时一并清除失败、回到 off。 */
export function dismissTunNotice() {
  useTun.setState({ notice: null })
  if (useTun.getState().status.state === 'failed') stopTun()
}

/** 在主窗口挂载一次：拉取初始状态、订阅 tun_state 并定时回查，开启、关闭期间缩短回查间隔。 */
export function useTunSync() {
  const interval = useTun((s) =>
    isTunTransient(tunPhaseOf(s.status, s.stopPending)) ? TUN_TRANSIENT_SYNC_MS : TUN_STABLE_SYNC_MS,
  )
  useEffect(() => {
    void refreshTunStatus()
    return Events.On('tun_state', (e: { data?: TunStatus }) => {
      if (e.data) acceptTunStatus(e.data)
    })
  }, [])
  useEffect(() => pollSnapshot(interval, Bridge.getTunStatus, acceptTunStatus), [interval])
}
