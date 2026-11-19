import type { UpdateState } from '@/lib/bridge'

export const TRANSIENT_SYNC_MS = 3000
export const STABLE_SYNC_MS = 60_000

/** 事件与调用结果可能乱序，只采纳修订号更高的快照。 */
export function newerUpdateState(prev: UpdateState, next: UpdateState): UpdateState {
  return next.revision > prev.revision ? next : prev
}

/** 检查与下载期间状态很快会自行变化，推送丢失时要尽快对上；稳定状态也要回查：自动检查的开始与结束推送都丢掉时，界面只能靠它看到新版。 */
export function updateSyncInterval(status: UpdateState['status']): number {
  return status === 'checking' || status === 'downloading' ? TRANSIENT_SYNC_MS : STABLE_SYNC_MS
}
