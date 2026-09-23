/**
 * 长连接会话增量推送的合并规则。
 *
 * 后端每帧只推新增的那一条（见 types 里的 SessionDelta），两个消费端——流量列表的
 * useBackendSync 与构造器的 useOutbound——必须按同一套规则合回整条会话，否则同一条流
 * 在两个界面里会长得不一样。
 */
import type { SessionDelta, StreamDelta, WsDelta } from '@/types'

/** 合并结果。gap 为真表示事件总线丢过消息，本地时间线已经缺帧，调用方须整条重拉。 */
export interface Merged<S> {
  session: S
  gap: boolean
}

/**
 * retained 缺失时的兜底条数上限（对齐 flow.MaxWSMessages / MaxStreamMessages）。
 *
 * 正常路径永远用后端给的 retained；这个常量只防一件事——万一哪天该字段没送到，
 * 本地时间线就会无界增长。
 */
const FALLBACK_MAX_MESSAGES = 500

/**
 * 把一条增量并进已有会话（prev 为空即首次见到，直接建）。
 *
 * 裁剪按后端给的 retained 走，而不是在前端另写一套上限：后端是按字节预算淘汰的，
 * 而这里拿到的 data 已被预览截断，两边各算各的必然对不上。
 */
export function mergeDelta<S extends { messageCount: number; messages: M[] }, M>(
  prev: S | undefined,
  delta: SessionDelta<S, M>,
): Merged<S> {
  const next = { ...delta.session }
  if (!prev) {
    next.messages = delta.message ? [delta.message] : []
    return { session: next, gap: false }
  }

  // 号回落即陈旧推送，整条丢弃：整条回填（getWSSession）与增量事件是两条独立通路，
  // 回填赶在一条早发的增量之前落地时会撞上。这类推送带的每一样东西手上都已经有了
  // 更新的版本——照收只会把消息重复一遍，还把 messageCount 拨回去，让下一条真增量误判成跳号。
  //
  // 判据里的 message 不可省：关闭 / 补进程这类只动元数据的推送本就不推进 messageCount，
  // 它们的号「不增长」是正常的，必须照常应用。
  if (delta.message && delta.session.messageCount <= prev.messageCount) {
    return { session: prev, gap: false }
  }

  // 跳号 = 总线丢过事件（core.EventBus 对慢订阅者直接丢弃），本地已经缺帧。
  const gap = !!delta.message && delta.session.messageCount > prev.messageCount + 1

  const cap = Number.isFinite(delta.retained) ? delta.retained : FALLBACK_MAX_MESSAGES
  const messages = delta.message ? [...prev.messages, delta.message] : prev.messages
  next.messages = messages.length > cap ? messages.slice(messages.length - cap) : messages
  return { session: next, gap }
}

export const mergeWsDelta = (prev: WsDelta['session'] | undefined, d: WsDelta) => mergeDelta(prev, d)
export const mergeStreamDelta = (prev: StreamDelta['session'] | undefined, d: StreamDelta) =>
  mergeDelta(prev, d)

/**
 * 整条重拉的结果能否落地。
 *
 * 重拉与增量是两条独立通路：拉取发出之后、结果落地之前到达的增量已经合进本地时间线，
 * 此时把旧快照整条盖上去等于把刚收到的帧丢掉，还会把 messageCount 拨回去，
 * 让下一条增量再判一次跳号——重拉于是自己把自己触发下去。
 * 判据与 mergeDelta 里「号回落即陈旧推送」是同一条。
 */
export function acceptsRefetch<S extends { messageCount: number }>(
  prev: S | undefined,
  fetched: S,
): boolean {
  return !prev || fetched.messageCount >= prev.messageCount
}

/**
 * 一次性往返的整条重拉能否落地。HTTP 会话没有序号可比，只能按状态判：
 * 后端只会从 pending 走向终态，故手上已有一份时，只有终态快照值得覆盖——
 * 两边都还是 pending 时内容没变，落地只会白白触发一次重渲染（轮询每 2s 一次）。
 */
export function acceptsHttpRefetch(
  prev: { status?: string } | undefined,
  fetched: { status?: string },
): boolean {
  return !prev || (fetched.status ?? 'pending') !== 'pending'
}

/**
 * 丢帧后的整条重拉，同一 id 同时只跑一次。
 *
 * 没有这道闸门的话，一次总线拥塞会让每条后续增量都判定为跳号、各自发起一次重拉，
 * 反过来把拥塞坐实。
 * onMissing 只在查询成功返回空时调用，查询失败仍可重试。
 */
export function createRefetcher<T>(fetch: (id: string) => Promise<T | null>) {
  const inflight = new Set<string>()
  return (id: string, apply: (value: T) => void, onMissing?: () => void) => {
    if (inflight.has(id)) return
    inflight.add(id)
    void fetch(id)
      .then((value) => {
        if (value) apply(value)
        else onMissing?.()
      })
      .catch(() => {
        /* 拉不到就等下一条增量，界面缺一帧好过整条卡住 */
      })
      .finally(() => inflight.delete(id))
  }
}
