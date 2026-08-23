import test from 'node:test'
import assert from 'node:assert/strict'
import { acceptsHttpRefetch, acceptsRefetch, createRefetcher, mergeDelta } from './delta.ts'

/** 最小可用的会话/消息形状，够 mergeDelta 的泛型约束用。 */
interface Sess {
  id: string
  status: string
  messageCount: number
  messages: Msg[]
}
interface Msg {
  id: string
}

function sess(messageCount: number, status = 'open'): Sess {
  return { id: 's1', status, messageCount, messages: [] }
}

function delta(messageCount: number, msgId?: string, retained = 500, status = 'open') {
  return {
    session: sess(messageCount, status),
    message: msgId ? { id: msgId } : undefined,
    retained,
  }
}

const ids = (s: Sess) => s.messages.map((m) => m.id)

test('首次见到一条会话时直接建，首帧即时间线的开头', () => {
  const { session, gap } = mergeDelta<Sess, Msg>(undefined, delta(1, 'a'))
  assert.deepEqual(ids(session), ['a'])
  assert.equal(gap, false)
})

test('元数据-only 的推送建出的是空时间线', () => {
  const { session } = mergeDelta<Sess, Msg>(undefined, delta(0))
  assert.deepEqual(ids(session), [])
})

test('连号推送逐条追加，不重发历史', () => {
  let cur = mergeDelta<Sess, Msg>(undefined, delta(1, 'a')).session
  for (const [n, id] of [
    [2, 'b'],
    [3, 'c'],
  ] as const) {
    const r = mergeDelta<Sess, Msg>(cur, delta(n, id))
    assert.equal(r.gap, false, `第 ${n} 条不该判为跳号`)
    cur = r.session
  }
  assert.deepEqual(ids(cur), ['a', 'b', 'c'])
  assert.equal(cur.messageCount, 3)
})

test('跳号即总线丢过事件：报 gap，同时照常收下手上这条', () => {
  const prev = { ...sess(1), messages: [{ id: 'a' }] }
  // messageCount 从 1 跳到 4：中间的 2、3 丢了。
  const { session, gap } = mergeDelta<Sess, Msg>(prev, delta(4, 'd'))
  assert.equal(gap, true)
  assert.deepEqual(ids(session), ['a', 'd'], '缺帧要靠重拉补，但手上这条不能丢')
})

test('号回落的陈旧推送整条丢弃，不重复也不把号拨回去', () => {
  const prev = { ...sess(5), messages: [{ id: 'a' }, { id: 'b' }] }
  const { session, gap } = mergeDelta<Sess, Msg>(prev, delta(3, 'b'))
  assert.equal(session, prev, '陈旧推送应原样返回上一版，连新对象都不必建')
  assert.equal(gap, false)

  // 拨回去的话，下一条真增量（6）会被误判成跳号。
  const next = mergeDelta<Sess, Msg>(session, delta(6, 'c'))
  assert.equal(next.gap, false)
  assert.deepEqual(ids(next.session), ['a', 'b', 'c'])
})

test('只动元数据的推送号不增长，但必须照常应用', () => {
  const prev = { ...sess(2), messages: [{ id: 'a' }, { id: 'b' }] }
  const { session, gap } = mergeDelta<Sess, Msg>(prev, delta(2, undefined, 2, 'closed'))
  assert.equal(gap, false)
  assert.equal(session.status, 'closed', '关闭这类更新不带消息，不能被陈旧判定拦下')
  assert.deepEqual(ids(session), ['a', 'b'])
})

test('本地时间线裁到后端给的 retained', () => {
  const prev = { ...sess(3), messages: [{ id: 'a' }, { id: 'b' }, { id: 'c' }] }
  const { session } = mergeDelta<Sess, Msg>(prev, delta(4, 'd', 2))
  assert.deepEqual(ids(session), ['c', 'd'], '按后端的保留条数淘汰最旧的')
})

test('retained 缺失时退回条数上限，不让时间线无界增长', () => {
  const messages = Array.from({ length: 600 }, (_, i) => ({ id: `m${i}` }))
  const prev = { ...sess(600), messages }
  const broken = { session: sess(601), message: { id: 'new' } } as unknown as Parameters<
    typeof mergeDelta<Sess, Msg>
  >[1]
  const { session } = mergeDelta<Sess, Msg>(prev, broken)
  assert.equal(session.messages.length, 500)
  assert.equal(session.messages.at(-1)?.id, 'new')
})

test('同一条会话的重拉同时只跑一次，一次拥塞不会引出一串重拉', async () => {
  let calls = 0
  const release = new Map<string, (v: { id: string } | null) => void>()
  const refetch = createRefetcher<{ id: string }>((id) => {
    calls++
    return new Promise((resolve) => release.set(id, resolve))
  })

  const got: string[] = []
  refetch('s1', (v) => got.push(v.id))
  refetch('s1', (v) => got.push(v.id))
  refetch('s2', (v) => got.push(v.id))
  assert.equal(calls, 2, 's1 在途时的第二次应被闸门挡下，s2 不受影响')

  release.get('s1')?.({ id: 's1' })
  await new Promise((r) => setImmediate(r))
  assert.deepEqual(got, ['s1'])

  // 在途结束后同一条可以再拉。
  refetch('s1', () => {})
  assert.equal(calls, 3)
})

test('重拉失败不抛，界面缺一帧好过整条卡住', async () => {
  const refetch = createRefetcher<{ id: string }>(() => Promise.reject(new Error('boom')))
  let applied = false
  refetch('s1', () => {
    applied = true
  })
  await new Promise((r) => setImmediate(r))
  assert.equal(applied, false)
})

test('首次重拉无条件落地，之后只认不倒退的快照', () => {
  assert.equal(acceptsRefetch(undefined, sess(3)), true, '手上还没有时拉到什么都收下')
  assert.equal(acceptsRefetch(sess(3), sess(5)), true)
  assert.equal(acceptsRefetch(sess(3), sess(3)), true, '同号是同一份，覆盖无害')
  // 拉取发出后、结果落地前到达的增量已经把号推到 5，此时落地一份 3 就是把刚收到的帧丢掉，
  // 还会让下一条增量再判一次跳号——重拉自己把自己触发下去。
  assert.equal(acceptsRefetch(sess(5), sess(3)), false)
})

test('HTTP 重拉只有终态值得覆盖', () => {
  assert.equal(acceptsHttpRefetch(undefined, { status: 'pending' }), true)
  // 事件先到、回填后到：一份 pending 的快照绝不能盖掉已经收到的终态，
  // 否则构造器页签会永远停在「发送中」。
  assert.equal(acceptsHttpRefetch({ status: 'completed' }, { status: 'pending' }), false)
  assert.equal(acceptsHttpRefetch({ status: 'pending' }, { status: 'completed' }), true)
  assert.equal(acceptsHttpRefetch({ status: 'pending' }, { status: 'error' }), true)
  // 两边都还是 pending：内容没变，落地只会白白触发一次重渲染（轮询每 2s 一次）。
  assert.equal(acceptsHttpRefetch({ status: 'pending' }, { status: 'pending' }), false)
})
