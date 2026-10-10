import test from 'node:test'
import assert from 'node:assert/strict'
import {
  bodyEditorOf,
  editBodyParameters,
  editRawBody,
  switchBodyMode,
} from './body.ts'
import { newDraft, newHeaderRow, wireHeadersFrom, type Draft } from './model.ts'
import { newParameterRow } from './parameters.ts'
import { parseCurl } from './curl.ts'

function withBody(body: string, contentType?: string): Draft {
  return {
    ...newDraft(),
    body,
    headers: contentType ? [newHeaderRow('Content-Type', contentType)] : [],
  }
}

function apply(draft: Draft, patch: Partial<Draft>): Draft {
  return { ...draft, ...patch }
}

function contentTypes(draft: Draft): string[] {
  return draft.headers
    .filter(row => row.name.trim().toLowerCase() === 'content-type')
    .map(row => row.value)
}

function multipartForm(draft: Draft): Promise<FormData> {
  return new Request('http://example.test', {
    method: 'POST',
    headers: { 'Content-Type': contentTypes(draft)[0] },
    body: draft.body,
  }).formData()
}

test('读取捕获正文只推断格式，保留原始头部与正文字节', () => {
  for (const [contentType, body, mode] of [
    ['application/problem+json; charset=utf-8', '{ "错误": true }\r\n', 'json'],
    ['application/soap+xml', '<?xml version="1.0"?><a/>\r\n', 'xml'],
    ['text/xml', '<a/>', 'xml'],
    ['text/html; charset=UTF-8', '<p>Hello</p>', 'html'],
    ['text/csv', 'a,b\r\n1,2\r\n', 'text'],
    [
      'application/x-www-form-urlencoded',
      'a=%20&a=2&flag&%E4%B8%AD=%E6%96%87',
      'urlencoded',
    ],
  ] as const) {
    const draft = withBody(body, contentType)
    const before = structuredClone(draft)
    assert.equal(bodyEditorOf(draft).mode, mode)
    assert.deepEqual(draft, before)
    assert.deepEqual(editRawBody(draft, body), {})
  }
})

test('格式切换更新单个 Content-Type，并保留其他头的重复次序与字节旁路', () => {
  const draft = withBody('{}', 'application/json')
  const opaque = {
    ...newHeaderRow('X-Opaque', '\\xff'),
    enc: 'bytes' as const,
    orig: { text: '�', b64: '/w==' },
  }
  draft.headers.push(
    opaque,
    newHeaderRow('CONTENT-TYPE', 'text/plain'),
    newHeaderRow('X-Opaque', 'two')
  )
  const changed = apply(draft, switchBodyMode(draft, 'xml'))
  assert.deepEqual(contentTypes(changed), ['application/xml'])
  assert.equal(
    changed.headers.find(row => row.id === opaque.id),
    opaque
  )
  const wire = wireHeadersFrom(changed.headers)
  assert.deepEqual(wire.pairs, [
    ['Content-Type', 'application/xml'],
    ['X-Opaque', '�'],
    ['X-Opaque', 'two'],
  ])
  assert.deepEqual(wire.valuesB64, ['', '/w==', ''])
})

test('表单编辑发送重复键、空键空值和中文，禁用行保存在草稿中', () => {
  let draft = newDraft()
  draft = apply(draft, switchBodyMode(draft, 'urlencoded'))
  const disabled = { ...newParameterRow('secret', '隐藏'), enabled: false }
  draft = apply(
    draft,
    editBodyParameters(draft, [
      newParameterRow('tag', '中文 空格'),
      newParameterRow('tag', 'second'),
      newParameterRow('empty', ''),
      newParameterRow('', '无名'),
      disabled,
    ])
  )
  assert.deepEqual(
    [...new URLSearchParams(draft.body)],
    [
      ['tag', '中文 空格'],
      ['tag', 'second'],
      ['empty', ''],
      ['', '无名'],
    ]
  )
  assert.equal(
    bodyEditorOf(draft).rows?.find(row => row.id === disabled.id)?.enabled,
    false
  )
  assert.deepEqual(contentTypes(draft), ['application/x-www-form-urlencoded'])
})

test('导入 urlencoded 后修改一个字段保留其他字段原编码', () => {
  let draft = withBody(
    'space=%20&flag&space=+&empty=',
    'application/x-www-form-urlencoded; charset=utf-8'
  )
  const rows = bodyEditorOf(draft).rows!
  draft = apply(
    draft,
    editBodyParameters(
      draft,
      rows.map(row => (row.name === 'empty' ? { ...row, value: '中文' } : row))
    )
  )
  assert.equal(draft.body, 'space=%20&flag&space=+&empty=%E4%B8%AD%E6%96%87')
  assert.deepEqual(contentTypes(draft), [
    'application/x-www-form-urlencoded; charset=utf-8',
  ])
})

test('None、原文格式与表单分别保留编辑内容，草稿之间独立', () => {
  let draft = withBody('{"a":1}', 'application/json')
  draft = apply(draft, switchBodyMode(draft, 'xml'))
  draft = apply(draft, editRawBody(draft, '<a>二</a>'))
  draft = apply(draft, switchBodyMode(draft, 'urlencoded'))
  draft = apply(draft, editBodyParameters(draft, [newParameterRow('a', '三')]))
  draft = apply(draft, switchBodyMode(draft, 'none'))
  assert.equal(draft.body, '')
  assert.deepEqual(contentTypes(draft), [])
  assert.equal(bodyEditorOf(draft).lastRawMode, 'xml')
  draft = apply(draft, switchBodyMode(draft, 'json'))
  assert.equal(draft.body, '{"a":1}')
  draft = apply(draft, switchBodyMode(draft, 'xml'))
  assert.equal(draft.body, '<a>二</a>')
  draft = apply(draft, switchBodyMode(draft, 'urlencoded'))
  assert.deepEqual([...new URLSearchParams(draft.body)], [['a', '三']])
  const other = newDraft()
  assert.equal(apply(other, switchBodyMode(other, 'json')).body, '')
})

test('表单格式首次互切复用字段，后续各自恢复正文与禁用状态', () => {
  let draft = withBody('a=1&a=2', 'application/x-www-form-urlencoded')
  const rows = bodyEditorOf(draft).rows!
  draft = apply(
    draft,
    editBodyParameters(
      draft,
      rows.map((row, index) => (index === 1 ? { ...row, enabled: false } : row))
    )
  )
  draft = apply(draft, switchBodyMode(draft, 'multipart'))
  assert.equal(bodyEditorOf(draft).rows?.[1].enabled, false)
  assert.match(draft.body, /name="a"\r\n\r\n1\r\n/)
  draft = apply(
    draft,
    editBodyParameters(
      draft,
      bodyEditorOf(draft).rows!.map((row, index) =>
        index === 0 ? { ...row, value: '变更' } : row
      )
    )
  )
  draft = apply(draft, switchBodyMode(draft, 'urlencoded'))
  assert.equal(draft.body, 'a=1')
  assert.equal(bodyEditorOf(draft).rows?.[1].enabled, false)
  draft = apply(draft, switchBodyMode(draft, 'multipart'))
  assert.match(draft.body, /\r\n变更\r\n/)
})

test('multipart 可由标准表单解析器读取，定界符稳定且字段名不能注入头', async () => {
  let draft = newDraft()
  draft = apply(draft, switchBodyMode(draft, 'multipart'))
  const boundary = bodyEditorOf(draft).boundary
  draft = apply(
    draft,
    editBodyParameters(draft, [
      newParameterRow('tag', '一'),
      newParameterRow('tag', ''),
      newParameterRow('quote"\r\nX-Injected: yes', 'value'),
      newParameterRow('back\\slash', 'v'),
    ])
  )
  assert.equal(bodyEditorOf(draft).boundary, boundary)
  assert.ok(draft.body.endsWith(`--${boundary}--\r\n`))
  assert.ok(!draft.body.includes('\r\nX-Injected:'))
  const form = await multipartForm(draft)
  assert.deepEqual(form.getAll('tag'), ['一', ''])
  assert.equal(form.get('quote"\r\nX-Injected: yes'), 'value')
  assert.equal(form.get('back\\slash'), 'v')
})

test('字段值包含已有 multipart 定界符时更换定界符并保持完整字段', async () => {
  let draft = newDraft()
  draft = apply(draft, switchBodyMode(draft, 'multipart'))
  const boundary = bodyEditorOf(draft).boundary!
  const value = `before\r\n--${boundary}\r\nafter`
  draft = apply(
    draft,
    editBodyParameters(draft, [newParameterRow('data', value)])
  )
  assert.notEqual(bodyEditorOf(draft).boundary, boundary)
  assert.equal((await multipartForm(draft)).get('data'), value)
})

test('cURL 文本 multipart 导入可编辑，读取时保留已有 LF 与 boundary', () => {
  const imported = parseCurl(
    "curl 'https://example.test' -F 'tag=一' -F 'tag=二'"
  )
  assert.equal(imported.ok, true)
  if (!imported.ok) return
  const original = structuredClone(imported.draft)
  const state = bodyEditorOf(imported.draft)
  assert.equal(state.mode, 'multipart')
  assert.equal(state.rawMultipart, false)
  assert.deepEqual(
    state.rows?.filter(row => row.name).map(row => [row.name, row.value]),
    [
      ['tag', '一'],
      ['tag', '二'],
    ]
  )
  assert.deepEqual(imported.draft, original)
  const changed = apply(
    imported.draft,
    editBodyParameters(
      imported.draft,
      state.rows!.map(row =>
        row.name ? { ...row, value: row.value + '!' } : row
      )
    )
  )
  assert.equal(bodyEditorOf(changed).boundary, state.boundary)
  assert.ok(changed.body.includes('\r\n'))
})

test('含文件或附加 part 头的 multipart 保留原文，可切换后恢复', () => {
  const body =
    '--captured\r\nContent-Disposition: form-data; name="file"; filename="a.txt"\r\nContent-Type: text/plain\r\n\r\nhello\r\n--captured--\r\n'
  let draft = withBody(body, 'multipart/form-data; boundary="captured"')
  assert.equal(bodyEditorOf(draft).rawMultipart, true)
  assert.deepEqual(editRawBody(draft, body), {})
  draft = apply(draft, switchBodyMode(draft, 'none'))
  draft = apply(draft, switchBodyMode(draft, 'multipart'))
  assert.equal(draft.body, body)
  assert.deepEqual(contentTypes(draft), [
    'multipart/form-data; boundary="captured"',
  ])
  draft = apply(draft, editRawBody(draft, body.replace('hello', 'world')))
  assert.equal(bodyEditorOf(draft).rawMultipart, true)
  assert.deepEqual(contentTypes(draft), [
    'multipart/form-data; boundary="captured"',
  ])
})

test('multipart 转义字段名保留原文，切换格式不会转换部分字段或误编码键名', () => {
  for (const name of [
    String.raw`a\\b`,
    String.raw`a\"b`,
    String.raw`a\b`,
    'trailing\\',
  ]) {
    const body =
      '--captured\r\nContent-Disposition: form-data; name="plain"\r\n\r\nfirst\r\n' +
      `--captured\r\nContent-Disposition: form-data; name="${name}"\r\n\r\nsecond\r\n--captured--\r\n`
    const contentType = 'multipart/form-data; boundary="captured"'
    let draft = withBody(body, contentType)
    const original = structuredClone(draft)
    const editor = bodyEditorOf(draft)
    assert.equal(editor.rawMultipart, true, name)
    assert.equal(editor.rows, undefined, name)
    assert.deepEqual(draft, original)

    draft = apply(draft, switchBodyMode(draft, 'urlencoded'))
    assert.equal(draft.body, '')
    draft = apply(draft, switchBodyMode(draft, 'multipart'))
    assert.equal(draft.body, body)
    assert.deepEqual(contentTypes(draft), [contentType])
    assert.equal(bodyEditorOf(draft).rawMultipart, true)
  }
})

test('表格创建的反斜杠字段名转换为 URL 编码表单时保持字段语义', () => {
  let draft = newDraft()
  draft = apply(draft, switchBodyMode(draft, 'multipart'))
  draft = apply(
    draft,
    editBodyParameters(draft, [newParameterRow('a\\b', 'value')])
  )
  draft = apply(draft, switchBodyMode(draft, 'urlencoded'))
  assert.equal(draft.body, 'a%5Cb=value')
  assert.deepEqual([...new URLSearchParams(draft.body)], [['a\\b', 'value']])
})

test('手动改 Content-Type 会重新推断格式，并保留此前格式草稿', () => {
  let draft = withBody('{"a":1}', 'application/json')
  draft = apply(draft, editRawBody(draft, '{"a":2}'))
  draft = {
    ...draft,
    headers: [newHeaderRow('Content-Type', 'application/xml')],
  }
  assert.equal(bodyEditorOf(draft).mode, 'xml')
  draft = apply(draft, editRawBody(draft, '<a/>'))
  draft = apply(draft, switchBodyMode(draft, 'json'))
  assert.equal(draft.body, '{"a":2}')
  assert.deepEqual(contentTypes(draft), ['application/json'])
})

test('恢复缺少 Content-Type 的 JSON 草稿会设置对应格式的头', () => {
  let draft = withBody('{"a":1}')
  draft = apply(draft, switchBodyMode(draft, 'none'))
  draft = apply(draft, switchBodyMode(draft, 'json'))
  assert.equal(draft.body, '{"a":1}')
  assert.deepEqual(contentTypes(draft), ['application/json'])
})

test('已有空头占位行不会因添加 Content-Type 留到中间', () => {
  let draft = newDraft()
  draft.headers = [newHeaderRow('X-Test', 'value'), newHeaderRow()]
  draft = apply(draft, switchBodyMode(draft, 'json'))
  assert.deepEqual(
    draft.headers.map(row => row.name),
    ['X-Test', 'Content-Type', '']
  )
})

test('空键空值是真实表单字段，空查询分隔符不变成 multipart 字段', async () => {
  let draft = withBody('=&a=1&&', 'application/x-www-form-urlencoded')
  draft = apply(draft, switchBodyMode(draft, 'multipart'))
  assert.deepEqual(
    [...(await multipartForm(draft))],
    [
      ['', ''],
      ['a', '1'],
    ]
  )
  draft = { ...draft, bodyEditor: undefined }
  const state = bodyEditorOf(draft)
  draft = apply(
    draft,
    editBodyParameters(
      draft,
      state.rows!.map(row => (row.name === 'a' ? { ...row, value: '2' } : row))
    )
  )
  assert.deepEqual(
    [...(await multipartForm(draft))],
    [
      ['', ''],
      ['a', '2'],
    ]
  )
})

test('multipart 调整头参数仍保留禁用字段，更换冲突边界满足长度限制', () => {
  let draft = withBody('', `multipart/form-data; boundary=${'b'.repeat(70)}`)
  draft = apply(
    draft,
    editBodyParameters(draft, [
      newParameterRow('a', '1'),
      { ...newParameterRow('hidden', '2'), enabled: false },
    ])
  )
  draft = {
    ...draft,
    headers: draft.headers.map(row =>
      row.name === 'Content-Type'
        ? { ...row, value: row.value + '; charset=utf-8' }
        : row
    ),
  }
  assert.equal(
    bodyEditorOf(draft).rows?.find(row => row.name === 'hidden')?.enabled,
    false
  )
  const conflicting = newParameterRow('collision', 'b'.repeat(70))
  draft = apply(draft, editBodyParameters(draft, [conflicting]))
  assert.ok(bodyEditorOf(draft).boundary!.length <= 70)
  assert.notEqual(bodyEditorOf(draft).boundary, 'b'.repeat(70))
})

test('导入带空格和特殊字符的 multipart boundary 后编辑仍可由标准解析器读取', async () => {
  for (const boundary of ['a b', 'a:b/c?d=(e)']) {
    const body = `--${boundary}\r\nContent-Disposition: form-data; name="a"\r\n\r\n1\r\n--${boundary}--\r\n`
    let draft = withBody(body, `multipart/form-data; boundary="${boundary}"`)
    draft = apply(
      draft,
      editBodyParameters(
        draft,
        bodyEditorOf(draft).rows!.map(row =>
          row.name === 'a' ? { ...row, value: '变更' } : row
        )
      )
    )
    assert.equal((await multipartForm(draft)).get('a'), '变更')
    assert.equal(bodyEditorOf(draft).boundary, boundary)
  }
})
