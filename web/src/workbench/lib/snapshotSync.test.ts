import test from 'node:test'
import assert from 'node:assert/strict'
import { pollSnapshot } from './snapshotSync.ts'

/** 让已经 resolve 的 promise 回调跑完。setImmediate 不在 mock 范围内。 */
const flush = () => new Promise<void>((resolve) => setImmediate(resolve))

test('按给定间隔回查并采纳结果', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  const applied: number[] = []
  let next = 0
  const stop = pollSnapshot(1000, async () => ++next, (s) => applied.push(s))

  t.mock.timers.tick(999)
  await flush()
  assert.deepEqual(applied, [], '不到间隔不该回查')
  t.mock.timers.tick(1)
  await flush()
  t.mock.timers.tick(1000)
  await flush()
  stop()
  assert.deepEqual(applied, [1, 2])
})

test('回查失败不影响下一次', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  const applied: string[] = []
  let calls = 0
  const stop = pollSnapshot(
    1000,
    async () => {
      if (++calls === 1) throw new Error('后端未就绪')
      return 'ok'
    },
    (s) => applied.push(s),
  )
  t.mock.timers.tick(1000)
  await flush()
  t.mock.timers.tick(1000)
  await flush()
  stop()
  assert.deepEqual(applied, ['ok'])
})

test('清理后途中返回的结果作废且不再回查', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  let calls = 0
  let reply: (s: number) => void = () => {}
  const applied: number[] = []
  const stop = pollSnapshot(
    1000,
    () => {
      calls++
      return new Promise<number>((resolve) => (reply = resolve))
    },
    (s) => applied.push(s),
  )

  t.mock.timers.tick(1000)
  assert.equal(calls, 1)
  stop()
  reply(9)
  await flush()
  assert.equal(applied.length, 0, '清理前发出的回查不该落定')

  t.mock.timers.tick(10_000)
  assert.equal(calls, 1, '清理后不该再回查')
})
