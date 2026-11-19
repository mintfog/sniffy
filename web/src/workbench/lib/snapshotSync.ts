/**
 * 后端事件总线会丢弃慢订阅者的消息，订阅了推送的快照还要定时回查以恢复同步。
 * 清理时停止定时器，并忽略尚未返回的请求结果。
 */
export function pollSnapshot<T>(intervalMs: number, fetch: () => Promise<T>, apply: (s: T) => void): () => void {
  let active = true
  const timer = setInterval(() => {
    fetch()
      .then((s) => {
        if (active) apply(s)
      })
      .catch(() => {})
  }, intervalMs)
  return () => {
    active = false
    clearInterval(timer)
  }
}
