import test from 'node:test'
import assert from 'node:assert/strict'
import type { UpdateState } from '@/lib/bridge'
import { STABLE_SYNC_MS, TRANSIENT_SYNC_MS, newerUpdateState, syncUpdateState } from './updateSync.ts'

function snapshot(revision: number, status: UpdateState['status']): UpdateState {
  return { revision, status, current: '1.0.0', notify: false, autoCheck: true, installAction: 'reveal' }
}

/** 让已经 resolve 的 promise 回调跑完。setImmediate 不在 mock 范围内。 */
const flush = () => new Promise<void>((resolve) => setImmediate(resolve))

// 初始快照、调用返回值与事件先后不定：旧的后到时不能盖掉新的。
test('只接受修订号更高的快照', () => {
  const current = snapshot(5, 'available')
  assert.equal(newerUpdateState(current, snapshot(4, 'idle')), current)
  assert.equal(newerUpdateState(current, snapshot(6, 'latest')).status, 'latest')
  assert.equal(newerUpdateState(current, snapshot(5, 'available')), current)
  assert.equal(newerUpdateState(snapshot(-1, 'idle'), snapshot(0, 'idle')).revision, 0)
})

test('检查中、下载中按短间隔对账', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  for (const status of ['checking', 'downloading'] as const) {
    let calls = 0
    const stop = syncUpdateState(status, async () => (calls++, snapshot(1, 'available')), () => {})
    t.mock.timers.tick(TRANSIENT_SYNC_MS)
    await flush()
    stop()
    assert.equal(calls, 1, `${status} 应在 ${TRANSIENT_SYNC_MS}ms 内对账`)
  }
})

// 自动检查的开始与结束事件都丢掉时，只有稳定状态下的对账能让界面看到新版。
test('稳定状态按长间隔对账', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  const applied: UpdateState[] = []
  const stop = syncUpdateState('idle', async () => snapshot(3, 'available'), (s) => applied.push(s))

  t.mock.timers.tick(TRANSIENT_SYNC_MS)
  await flush()
  assert.equal(applied.length, 0, '稳定状态不该按短间隔回查')

  t.mock.timers.tick(STABLE_SYNC_MS - TRANSIENT_SYNC_MS)
  await flush()
  stop()
  assert.deepEqual(
    applied.map((s) => s.status),
    ['available'],
  )
})

test('清理后途中返回的结果作废且不再对账', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  let calls = 0
  let reply: (s: UpdateState) => void = () => {}
  const applied: UpdateState[] = []
  const stop = syncUpdateState(
    'downloading',
    () => {
      calls++
      return new Promise<UpdateState>((resolve) => (reply = resolve))
    },
    (s) => applied.push(s),
  )

  t.mock.timers.tick(TRANSIENT_SYNC_MS)
  assert.equal(calls, 1)
  stop()
  reply(snapshot(9, 'available'))
  await flush()
  assert.equal(applied.length, 0, '清理前发出的对账不该落定')

  t.mock.timers.tick(STABLE_SYNC_MS)
  assert.equal(calls, 1, '清理后不该再对账')
})
