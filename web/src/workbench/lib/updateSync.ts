import type { UpdateState } from '@/lib/bridge'

export const TRANSIENT_SYNC_MS = 3000
export const STABLE_SYNC_MS = 60_000

/** 事件与调用结果可能乱序，只采纳修订号更高的快照。 */
export function newerUpdateState(prev: UpdateState, next: UpdateState): UpdateState {
  return next.revision > prev.revision ? next : prev
}

/**
 * 后端事件总线会丢弃慢订阅者的消息，定时回查以恢复同步；检查与下载期间缩短间隔。
 * 清理时停止定时器，并忽略尚未返回的请求结果。
 */
export function syncUpdateState(
  status: UpdateState['status'],
  fetchState: () => Promise<UpdateState>,
  apply: (s: UpdateState) => void,
): () => void {
  const interval = status === 'checking' || status === 'downloading' ? TRANSIENT_SYNC_MS : STABLE_SYNC_MS
  let active = true
  const timer = setInterval(() => {
    fetchState()
      .then((s) => {
        if (active) apply(s)
      })
      .catch(() => {})
  }, interval)
  return () => {
    active = false
    clearInterval(timer)
  }
}
