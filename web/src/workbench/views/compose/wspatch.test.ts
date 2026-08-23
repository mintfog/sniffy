/**
 * applyWsPatch 的回归锁：发帧是异步的，回调里的闭包快照已经过期。
 *
 * 由 node --test 直接加载 model.ts（不经打包），故这里不许出现 `@/` 运行期 import。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { applyWsPatch, newDraft, wsOf, type Draft } from './model.ts'

function wsDraft(ws: Partial<ReturnType<typeof wsOf>>): Draft {
  const d = newDraft('ws')
  return { ...d, ws: { ...wsOf(d), ...ws } }
}

test('A1 对象形态的补丁只覆盖给到的字段', () => {
  const d = wsDraft({ outgoing: 'hello', outgoingBinary: true, conn: 'open' })
  const next = applyWsPatch(d, { conn: 'closed' })
  assert.equal(wsOf(next).conn, 'closed')
  assert.equal(wsOf(next).outgoing, 'hello')
  assert.equal(wsOf(next).outgoingBinary, true)
})

test('A2 函数形态读到的是当前值而非调用方快照', () => {
  const d = wsDraft({ outgoing: '第二帧' })
  let seen = ''
  applyWsPatch(d, (prev) => {
    seen = prev.outgoing
    return {}
  })
  assert.equal(seen, '第二帧')
})

test('B1 发送成功后，内容没被改过就清空', () => {
  const sent = 'ping'
  const d = wsDraft({ outgoing: sent, conn: 'open' })
  const next = applyWsPatch(d, (prev) => (prev.outgoing === sent ? { outgoing: '' } : {}))
  assert.equal(wsOf(next).outgoing, '')
})

test('B2 等待期间键入的下一帧不被清空', () => {
  const sent = 'ping'
  // 用户在 await 期间把输入框改成了下一帧：清空条件不成立，内容必须原样留着。
  const d = wsDraft({ outgoing: '下一帧', conn: 'open' })
  const next = applyWsPatch(d, (prev) => (prev.outgoing === sent ? { outgoing: '' } : {}))
  assert.equal(wsOf(next).outgoing, '下一帧')
})

test('B3 等待期间切到二进制模式不被回滚', () => {
  const sent = 'ping'
  // 补丁只给出 outgoing 一项：等待期间切到二进制的开关不在补丁里，必须原样留着。
  const d = wsDraft({ outgoing: sent, outgoingBinary: true, conn: 'open' })
  const next = applyWsPatch(d, (prev) => (prev.outgoing === sent ? { outgoing: '' } : {}))
  assert.equal(wsOf(next).outgoingBinary, true)
  assert.equal(wsOf(next).outgoing, '')
})

test('C1 ws 分片缺省时按空值合并，不会抛', () => {
  const d = newDraft('ws')
  const bare: Draft = { ...d, ws: undefined }
  const next = applyWsPatch(bare, { conn: 'connecting' })
  assert.equal(wsOf(next).conn, 'connecting')
  assert.equal(wsOf(next).outgoing, '')
})
