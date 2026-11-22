import test from 'node:test'
import assert from 'node:assert/strict'
import { MAX_TUN_BYPASS, parseTunBypass } from './tunBypass.ts'

test('按换行、空格、逗号拆分，跳过空项与重复项', () => {
  assert.deepEqual(parseTunBypass(' 10.0.0.0/8\n\n192.168.1.5, 2001:db8::/32，fe80::1\t10.0.0.0/8 '), {
    list: ['10.0.0.0/8', '192.168.1.5', '2001:db8::/32', 'fe80::1'],
  })
  assert.deepEqual(parseTunBypass('  \n'), { list: [] })
})

test('主机位不为零的网段原样交给后端清零', () => {
  assert.deepEqual(parseTunBypass('10.1.2.3/8'), { list: ['10.1.2.3/8'] })
})

test('指出第一处无效的项', () => {
  for (const value of [
    'example.com',
    '10.0.0.256',
    '010.0.0.1',
    '10.0.0',
    '10.0.0.0/33',
    '10.0.0.0/08',
    '10.0.0.0/',
    '2001:db8::/129',
    '2001:db8:::1',
    'fe80::1%eth0',
    '::1]?x',
    '::1]#x',
    '::1]:80?',
    '2001:db8::g',
    '::ffff:10.0.0.1',
    '::ffff:10.0.0.0/104',
  ]) {
    assert.deepEqual(parseTunBypass(`192.168.0.0/16\n${value}`), { problem: { kind: 'invalid', value } }, value)
  }
})

test('比 /2 更大的网段范围过大', () => {
  for (const value of ['0.0.0.0/0', '128.0.0.0/1', '::/1']) {
    assert.deepEqual(parseTunBypass(value), { problem: { kind: 'tooBroad', value } }, value)
  }
  assert.deepEqual(parseTunBypass('64.0.0.0/2'), { list: ['64.0.0.0/2'] })
})

test('条目超过上限', () => {
  const items = Array.from({ length: MAX_TUN_BYPASS }, (_, i) => `10.${i >> 8}.${i & 255}.0/24`)
  assert.equal(parseTunBypass(items.join('\n')).list?.length, MAX_TUN_BYPASS)
  assert.deepEqual(parseTunBypass([...items, '192.0.2.0/24'].join('\n')), { problem: { kind: 'tooMany', max: MAX_TUN_BYPASS } })
})
