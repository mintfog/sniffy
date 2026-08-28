/**
 * 验证构造器头值旁路的解析、编辑、改动比对与回程编码。
 * 断点编辑器与构造器共用 headerRowsFrom / wireHeadersFrom。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import type { ComposeSeed } from '@/lib/bridge'
import {
  badEscapeName,
  basisValueB64,
  draftDiff,
  draftFromSeed,
  hasByteRows,
  headerRowsFrom,
  newHeaderRow,
  rowBytes,
  rowValueB64,
  wireHeadersFrom,
  type HeaderRow,
} from './model.ts'

const CD_BYTES = [0x63, 0x61, 0x66, 0xe9, 0x2e, 0x70, 0x64, 0x66]
const CD_B64 = Buffer.from(CD_BYTES).toString('base64')
const CD_LOSSY = new TextDecoder().decode(Uint8Array.from(CD_BYTES))
const CD_ESCAPED = 'caf\\xE9.pdf'

const bytesRow = (name: string, value: string): HeaderRow => ({ ...newHeaderRow(name, value), enc: 'bytes' })

function seed(over: Partial<ComposeSeed> = {}): ComposeSeed {
  return {
    flowId: 'f1',
    method: 'GET',
    url: 'https://x.com/a',
    headers: [
      ['X-File', CD_LOSSY],
      ['Accept', '*/*'],
    ],
    headersB64: [CD_B64, ''],
    body: '',
    bodySize: 0,
    ...over,
  }
}

/* ── 解析 ── */

// 验证带旁路的行以字节模式打开并显示 \xNN 转义。
test('蓝本的旁路行以字节模式打开', () => {
  const d = draftFromSeed(seed())
  assert.equal(d.headers[0].enc, 'bytes')
  assert.equal(d.headers[0].value, CD_ESCAPED)
  assert.equal(d.headers[1].enc, undefined)
})

// 验证旁路长度不匹配时回退文本模式并清空旁路。
test('旁路长度与头对不上时整条丢弃', () => {
  const d = draftFromSeed(seed({ headersB64: [CD_B64] }))
  assert.equal(d.headers[0].enc, undefined)
  assert.equal(d.headers[0].value, CD_LOSSY)
  assert.deepEqual(d.seed?.headersB64, [], '蓝本也不留半份旁路，否则后续比对会拿它当基准')
})

// 验证缺少旁路字段时沿用文本路径。
test('后端不发旁路时优雅降级', () => {
  const d = draftFromSeed(seed({ headersB64: undefined, headers: [['Accept', '*/*']] }))
  assert.equal(d.headers[0].enc, undefined)
  assert.deepEqual(d.seed?.headersB64, [])
})

/* ── 改动比对 ── */

// 验证改动比对使用出线字节，原样打开的旁路行保持未改动。
test('原样打开的旁路行不被误报改动', () => {
  const d = draftFromSeed(seed())
  assert.equal(draftDiff(d).headers.size, 0)
})

// 验证单行编辑只标记对应行。
test('改了字节行只标那一行', () => {
  const d = draftFromSeed(seed())
  const edited = { ...d, headers: d.headers.map((r, i) => (i === 0 ? { ...r, value: 'x\\xFF' } : r)) }
  assert.deepEqual([...draftDiff(edited).headers], [d.headers[0].id])
})

// 验证蓝本与编辑行按同一出线字节表示比较。
test('蓝本值与编辑行的出线字节在同一个域里比较', () => {
  assert.equal(basisValueB64(CD_LOSSY, CD_B64), rowValueB64(bytesRow('X-File', CD_ESCAPED)))
  assert.equal(basisValueB64('*/*'), rowValueB64(newHeaderRow('Accept', '*/*')))
  assert.notEqual(basisValueB64(CD_LOSSY, CD_B64), basisValueB64(CD_LOSSY))
})

/* ── 回程编码 ── */

// 验证头部与旁路数组由同一行列表生成并保持下标对齐。
test('无名行同产同弃，两个数组逐位对齐', () => {
  const rows: HeaderRow[] = [bytesRow('', CD_ESCAPED), newHeaderRow('Accept', '*/*'), bytesRow('X-File', CD_ESCAPED)]
  assert.deepEqual(wireHeadersFrom(rows), {
    pairs: [
      ['Accept', '*/*'],
      ['X-File', CD_LOSSY],
    ],
    valuesB64: ['', CD_B64],
  })
})

// 验证手写字节行的明文槽使用 utf8Lossy，旁路保存精确字节。
test('手写的字节行，明文槽是 U+FFFD 形态，旁路才是精确字节', () => {
  const { pairs, valuesB64 } = wireHeadersFrom([bytesRow('X-File', CD_ESCAPED)])
  assert.deepEqual(pairs, [['X-File', CD_LOSSY]])
  assert.deepEqual(Buffer.from(valuesB64[0], 'base64'), Buffer.from(CD_BYTES))
})

// 验证未改动字节行回传后端提供的明文槽，保持 Go 与 TextDecoder 的替换形态。
test('未改动的字节行，明文槽原样 echo 后端发来的串', () => {
  const bytes = [0x61, 0xe9, 0x80, 0x62]
  const goSanitized = 'a\uFFFD\uFFFDb'
  assert.notEqual(
    new TextDecoder().decode(Uint8Array.from(bytes)),
    goSanitized,
    '两种替换规则必须真的不同，否则这条用例钉不住东西',
  )
  const rows = headerRowsFrom([['X-T', goSanitized]], [Buffer.from(bytes).toString('base64')])
  assert.deepEqual(wireHeadersFrom(rows).pairs, [['X-T', goSanitized]])
})

// 验证编辑后的字节行以新字节生成明文槽与旁路。
test('字节行被改过后，明文槽跟着新字节走', () => {
  const rows = headerRowsFrom([['X-File', CD_LOSSY]], [CD_B64])
  const { pairs, valuesB64 } = wireHeadersFrom([{ ...rows[0], value: 'x\\xE9' }])
  assert.deepEqual(pairs, [['X-File', new TextDecoder().decode(Uint8Array.from([0x78, 0xe9]))]])
  assert.deepEqual(Buffer.from(valuesB64[0], 'base64'), Buffer.from([0x78, 0xe9]))
})

// 验证文本行的旁路项为空串。
test('文本行的旁路项是空串', () => {
  assert.deepEqual(wireHeadersFrom([newHeaderRow('Accept', '*/*')]).valuesB64, [''])
})

// 验证解析后原样回传可逐字节还原。
test('解析 → 回传 一圈之后字节不变', () => {
  const rows = headerRowsFrom(
    [
      ['X-File', CD_LOSSY],
      ['Accept', '*/*'],
    ],
    [CD_B64, ''],
  )
  assert.deepEqual(wireHeadersFrom(rows).valuesB64, [CD_B64, ''])
})

/* ── 用户输入 ── */

// 验证字节行中的普通字符按 UTF-8 编码，\xNN 按单字节编码。
test('字节行里中文与 \\xNN 混写，中文按 UTF-8 出线', () => {
  const row = bytesRow('X-Note', '报表\\xE9.pdf')
  assert.equal(rowBytes(row).error, undefined)
  assert.equal(badEscapeName([row]), '')
  assert.deepEqual(
    rowBytes(row).bytes,
    Uint8Array.from([...new TextEncoder().encode('报表'), 0xe9, ...new TextEncoder().encode('.pdf')]),
  )
})

// 验证非法转义返回首个头名，供调用方禁用发送或放行。
test('转义写法非法时报出第一条头名', () => {
  const rows: HeaderRow[] = [newHeaderRow('Accept', '*/*'), bytesRow('X-Bad', 'a\\xZZ'), bytesRow('X-Also', '\\x')]
  assert.equal(badEscapeName(rows), 'X-Bad')
  assert.equal(badEscapeName([bytesRow('', 'a\\xZZ')]), '', '无名行不上线，不该拦住发送')
})

// 验证文本模式将 \xNN 作为字面文本。
test('文本行的 \\xNN 是字面文本', () => {
  assert.deepEqual(rowBytes(newHeaderRow('X-Path', 'C:\\x41')).bytes, new TextEncoder().encode('C:\\x41'))
})

// 验证只有带名称的字节行使草稿携带旁路。
test('无名字节行不算「这份草稿带字节」', () => {
  assert.equal(hasByteRows([bytesRow('', CD_ESCAPED)]), false)
  assert.equal(hasByteRows([bytesRow('X-File', CD_ESCAPED)]), true)
  assert.equal(hasByteRows([newHeaderRow('Accept', '*/*')]), false)
})
