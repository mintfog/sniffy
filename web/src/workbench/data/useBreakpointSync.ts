import { useEffect } from 'react'
import { Events } from '@wailsio/runtime'
import { useAppStore } from '@/store'
import { Bridge } from '@/lib/bridge'
import { parsePausedFlow, type PausedFlow } from '../views/breakpoints/model'

/** 对账间隔。断点是低频事件，5s 远短于用户从"命中"到"抬手点击"的时间。 */
const RECONCILE_MS = 5000

/** 记住多少条已解除的 id 用于挡迟到事件。这是短期记忆，不是审计日志。 */
const RESOLVED_MEMORY = 512

/**
 * 断点的常驻订阅与对账（应用级，与 useBackendSync 并列挂在根组件上）。
 *
 * 订阅必须常驻而不能挂在断点页里：命中往往发生在用户正盯着流量表的时候，
 * 组件卸载即取消订阅意味着切走一次就再也收不到。
 *
 * 事件本身也不可靠：引擎的 EventBus 是非阻塞扇出、订阅者通道满即丢，而断点风暴
 * （全局开关一开，一个页面几十条并发请求）恰恰最容易把它打满。所以除了事件之外还要
 * 定期按 GetBreakpoints 对账——被丢掉的那条会被按住整整一个超时周期，界面上却始终看不见。
 */
/** 队列是否与上一轮一致（顺序、id、阶段、截止时刻都没变）。 */
function sameQueue(a: PausedFlow[], b: PausedFlow[]): boolean {
  return (
    a.length === b.length &&
    a.every((x, i) => x.id === b[i].id && x.phase === b[i].phase && x.pausedUntil === b[i].pausedUntil)
  )
}

export function useBreakpointSync() {
  useEffect(() => {
    let alive = true

    // 已收到 resolved 的暂停。事件与快照之间没有顺序保证：续期会重发一次命中，
    // 它可能晚于同一条的放行到达；对账拿到的也可能是一份已经过期的列表。
    // 没有这道闸，界面上就会长出点不动的僵尸行。
    //
    // 必须按「id + 阶段」记账而不是只按 id：同一条 flow 会先后被请求断点和响应断点
    // 各按一次，id 是同一个。只记 id 的话，请求阶段放行之后，这条 flow 的响应阶段
    // 断点就再也不会出现在界面上，浏览器一路空转到超时。
    const resolved = new Set<string>()
    const key = (id: string, phase: string) => `${id}:${phase}`
    // 每条本地条目第一次被看见的时刻，用于在对账时区分"后端已放行"与"快照发出后才命中"。
    const seenAt = new Map<string, number>()

    const forget = (id: string, phase: string, resolution?: string) => {
      if (resolution) useAppStore.getState().noteResolved(id, resolution)
      resolved.add(key(id, phase))
      seenAt.delete(id)
      if (resolved.size > RESOLVED_MEMORY) {
        // Set 按插入序遍历，从头删就是 FIFO 淘汰。
        let excess = resolved.size - RESOLVED_MEMORY / 2
        for (const old of resolved) {
          if (excess-- <= 0) break
          resolved.delete(old)
        }
      }
      useAppStore.getState().removePausedFlow(id)
    }

    const upsert = (flow: PausedFlow) => {
      if (resolved.has(key(flow.id, flow.phase))) return
      if (!seenAt.has(flow.id)) seenAt.set(flow.id, Date.now())
      useAppStore.getState().upsertPausedFlow(flow)
    }

    /**
     * 以后端为准做集合差分，但保留"快照发出之后才命中"的条目。
     *
     * 直接用快照整体替换会踩两个对称的坑：快照在途中被放行的条目会复活成僵尸行，
     * 快照在途中新命中的条目会被当成"后端已无"抹掉——后者是把一条真被按住的请求
     * 从界面上删掉，比前者更糟。
     */
    // 对账可能并发在途（5 秒定时器与切窗各触发一次）。迟到的旧快照里还带着已经被
    // 放行的条目，落地就是把僵尸行加回来，要等下一轮才自愈。用世代号直接丢弃它。
    let issued = 0
    let applied = 0
    const reconcile = () => {
      const gen = ++issued
      const askedAt = Date.now()
      Bridge.getBreakpoints()
        .then((list) => {
          if (!alive || !Array.isArray(list) || gen < applied) return
          applied = gen
          const fresh = list
            .map(parsePausedFlow)
            .filter((p): p is PausedFlow => p !== null && !resolved.has(key(p.id, p.phase)))
          const byId = new Map(fresh.map((p) => [p.id, p]))
          const store = useAppStore.getState()

          // 先按本地既有次序走一遍（列表不因对账而重排），再把后端新增的追加在末尾。
          const next: PausedFlow[] = []
          for (const local of store.pausedFlows) {
            const backend = byId.get(local.id)
            if (backend) {
              next.push(backend)
              byId.delete(local.id)
            } else if ((seenAt.get(local.id) ?? 0) > askedAt) {
              next.push(local) // 快照发出之后才命中，后端当然还不知道
            } else {
              seenAt.delete(local.id)
            }
          }
          for (const remaining of byId.values()) {
            if (!seenAt.has(remaining.id)) seenAt.set(remaining.id, Date.now())
            next.push(remaining)
          }
          // 没变就别写:5 秒一次的无条件写入会每次都换一个新数组引用，
          // 而流量表的行是按 pausedFlows 派生的 —— 一条断点都没有时也会周期性地
          // 把整张表重算一遍。
          if (!sameQueue(store.pausedFlows, next)) store.setPausedFlows(next)
        })
        .catch(() => {
          // 非 Wails 环境（浏览器直开）：没有后端，保持空列表。
        })
    }

    reconcile()
    Bridge.getGlobalBreak()
      .then((g) => alive && g && useAppStore.getState().setGlobalBreak(g))
      .catch(() => {})

    const offs: Array<() => void> = []
    try {
      offs.push(
        Events.On('breakpoint_hit', (e) => {
          const flow = parsePausedFlow(e.data)
          if (flow) upsert(flow)
        }),
      )
      offs.push(
        Events.On('breakpoint_resolved', (e) => {
          const data = e.data as { id?: string; pausedAt?: string; resolution?: string } | null
          if (data?.id) forget(data.id, data.pausedAt ?? 'request', data.resolution)
        }),
      )
    } catch {
      // runtime 不可用
    }

    const timer = setInterval(reconcile, RECONCILE_MS)
    const onFocus = () => reconcile()
    window.addEventListener('focus', onFocus)

    return () => {
      alive = false
      clearInterval(timer)
      window.removeEventListener('focus', onFocus)
      for (const off of offs) {
        try {
          off()
        } catch {
          /* ignore */
        }
      }
    }
  }, [])
}
