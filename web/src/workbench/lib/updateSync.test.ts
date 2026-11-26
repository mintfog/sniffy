import test from 'node:test'
import assert from 'node:assert/strict'
import type { UpdateState } from '@/lib/bridge'
import { STABLE_SYNC_MS, TRANSIENT_SYNC_MS, newerUpdateState, updateSyncInterval } from './updateSync.ts'

function snapshot(revision: number, status: UpdateState['status']): UpdateState {
  return { revision, status, current: '1.0.0', notify: false, autoCheck: true, installAction: 'reveal' }
}

// 初始快照、调用返回值与事件先后不定：旧的后到时不能盖掉新的。
test('只接受修订号更高的快照', () => {
  const current = snapshot(5, 'available')
  assert.equal(newerUpdateState(current, snapshot(4, 'idle')), current)
  assert.equal(newerUpdateState(current, snapshot(6, 'latest')).status, 'latest')
  assert.equal(newerUpdateState(current, snapshot(5, 'available')), current)
  assert.equal(newerUpdateState(snapshot(-1, 'idle'), snapshot(0, 'idle')).revision, 0)
})

test('检查中、下载中按短间隔对账，其余按长间隔', () => {
  assert.equal(updateSyncInterval('checking'), TRANSIENT_SYNC_MS)
  assert.equal(updateSyncInterval('downloading'), TRANSIENT_SYNC_MS)
  for (const status of ['idle', 'latest', 'available', 'downloaded', 'error'] as const) {
    assert.equal(updateSyncInterval(status), STABLE_SYNC_MS, status)
  }
})
