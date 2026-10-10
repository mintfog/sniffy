import type { Draft, HeaderRow } from './model.ts'
import { appendHeader, normalizeHeaders, rowBytes } from './model.ts'
import {
  isBlankParameter,
  newParameterRow,
  normalizeParameters,
  parseParameters,
  serializeParameters,
  type ParameterRow,
} from './parameters.ts'

export type RawBodyMode = 'json' | 'text' | 'xml' | 'html'
export type BodyMode = 'none' | RawBodyMode | 'urlencoded' | 'multipart'

interface BodySnapshot {
  body: string
  contentType?: string
  rows?: ParameterRow[]
  boundary?: string
  /** 含文件、附加 part 头或无法完整解析的 multipart 保持原文编辑。 */
  rawMultipart?: boolean
}

export interface BodyEditorState extends BodySnapshot {
  mode: BodyMode
  lastRawMode?: RawBodyMode
  saved: Partial<Record<BodyMode, BodySnapshot>>
}

const CONTENT_TYPES: Record<Exclude<BodyMode, 'none'>, string> = {
  json: 'application/json',
  text: 'text/plain',
  xml: 'application/xml',
  html: 'text/html',
  urlencoded: 'application/x-www-form-urlencoded',
  multipart: 'multipart/form-data',
}

const isContentType = (row: HeaderRow) =>
  row.name.trim().toLowerCase() === 'content-type'

export function isRawBodyMode(mode: BodyMode): mode is RawBodyMode {
  return mode === 'json' || mode === 'text' || mode === 'xml' || mode === 'html'
}

function contentTypeOf(headers: HeaderRow[]): string | undefined {
  const row = headers.find(isContentType)
  return row ? new TextDecoder().decode(rowBytes(row).bytes) : undefined
}

function setContentType(headers: HeaderRow[], value?: string): HeaderRow[] {
  let found = false
  const rows: HeaderRow[] = []
  for (const row of headers) {
    if (!isContentType(row)) {
      rows.push(row)
      continue
    }
    if (found || value === undefined) continue
    found = true
    rows.push({ id: row.id, name: row.name, value })
  }
  return !found && value !== undefined
    ? appendHeader(rows, 'Content-Type', value)
    : normalizeHeaders(rows)
}

function inferMode(contentType: string | undefined, body: string): BodyMode {
  const type = contentType?.split(';', 1)[0].trim().toLowerCase() ?? ''
  if (type === 'application/json' || type.endsWith('+json')) return 'json'
  if (
    type === 'application/xml' ||
    type === 'text/xml' ||
    type.endsWith('+xml')
  )
    return 'xml'
  if (type === 'text/html') return 'html'
  if (type === 'application/x-www-form-urlencoded') return 'urlencoded'
  if (type === 'multipart/form-data') return 'multipart'
  if (type) return 'text'
  if (!body) return 'none'
  try {
    JSON.parse(body)
    return 'json'
  } catch {
    return 'text'
  }
}

function boundaryOf(contentType: string | undefined): string | undefined {
  const match = /(?:^|;)\s*boundary\s*=\s*(?:"([^"\r\n]+)"|([^;\s]+))/i.exec(
    contentType ?? ''
  )
  const boundary = match?.[1] ?? match?.[2]
  if (
    !boundary ||
    !/^[0-9A-Za-z'()+_,./:=? -]{1,70}$/.test(boundary) ||
    boundary.endsWith(' ')
  )
    return undefined
  return boundary
}

function multipartContentType(boundary: string): string {
  const parameter = /^[A-Za-z0-9'+_.-]+$/.test(boundary)
    ? boundary
    : `"${boundary}"`
  return `multipart/form-data; boundary=${parameter}`
}

function parseMultipart(
  body: string,
  boundary: string
): ParameterRow[] | undefined {
  const parts = body.split(`--${boundary}`)
  if (parts.shift() !== '' || !/^--(?:\r\n|\n)?$/.test(parts.pop() ?? ''))
    return undefined
  const rows: ParameterRow[] = []
  for (const part of parts) {
    const newline = part.startsWith('\r\n') ? '\r\n' : '\n'
    if (!part.startsWith(newline) || !part.endsWith(newline)) return undefined
    const text = part.slice(newline.length, -newline.length)
    const separator = text.indexOf(newline + newline)
    if (separator < 0) return undefined
    const disposition =
      /^Content-Disposition:[ \t]*form-data;[ \t]*name="([^"\r\n]*)"[ \t]*$/i.exec(
        text.slice(0, separator)
      )
    if (!disposition) return undefined
    const name = disposition[1]
    // MIME 转义与浏览器原样保留的反斜杠存在歧义，保留原文以免改写字段名。
    if (name.includes('\\')) return undefined
    const value = text.slice(separator + newline.length * 2)
    const row = newParameterRow(name, value)
    // 沿用 URL 编码的空字段标记，避免真实空字段被当作表格占位行。
    if (name === '' && value === '') row.original = { name, value, raw: '=' }
    rows.push(row)
  }
  return normalizeParameters(rows)
}

function activeParameter(row: ParameterRow): boolean {
  // URL 编码中的空 & 片段只保留分隔，不对应 multipart 字段。
  return (
    row.enabled &&
    !isBlankParameter(row) &&
    (row.name !== '' || row.value !== '' || row.original?.raw !== '')
  )
}

function multipartBody(
  rows: ParameterRow[],
  previousBoundary?: string
): { body: string; boundary: string } {
  const active = rows.filter(activeParameter)
  let boundary =
    previousBoundary ?? `----sniffy-${Math.random().toString(36).slice(2)}`
  const prefix = boundary.slice(0, 60)
  let attempt = 0
  while (
    active.some(
      ({ name, value }) => name.includes(boundary) || value.includes(boundary)
    )
  ) {
    attempt += 1
    boundary = `${prefix}-${attempt}`
  }
  const parts = active.map(({ name, value }) => {
    // 与浏览器 FormData 一样编码引号和换行，防止字段名变成额外的 part 头。
    const escaped = name
      .replace(/\r/g, '%0D')
      .replace(/\n/g, '%0A')
      .replace(/"/g, '%22')
    return `--${boundary}\r\nContent-Disposition: form-data; name="${escaped}"\r\n\r\n${value}\r\n`
  })
  return { body: parts.join('') + `--${boundary}--\r\n`, boundary }
}

function snapshot(state: BodyEditorState): BodySnapshot {
  const { body, contentType, rows, boundary, rawMultipart } = state
  return { body, contentType, rows, boundary, rawMultipart }
}

/** bodyEditorOf 只推断编辑状态，导入请求在用户编辑前保持原始正文与头部。 */
export function bodyEditorOf(draft: Draft): BodyEditorState {
  const previous = draft.bodyEditor
  const contentType = contentTypeOf(draft.headers)
  if (
    previous &&
    previous.body === draft.body &&
    previous.contentType === contentType
  )
    return previous
  const mode = inferMode(contentType, draft.body)
  if (
    previous &&
    previous.mode === mode &&
    previous.body === draft.body &&
    (mode !== 'multipart' || boundaryOf(contentType) === previous.boundary)
  ) {
    return { ...previous, contentType }
  }
  const saved = previous
    ? { ...previous.saved, [previous.mode]: snapshot(previous) }
    : {}
  const state: BodyEditorState = {
    mode,
    body: draft.body,
    contentType,
    saved,
    lastRawMode: isRawBodyMode(mode) ? mode : previous?.lastRawMode,
  }
  if (mode === 'urlencoded') state.rows = parseParameters(draft.body)
  if (mode === 'multipart') {
    state.boundary = boundaryOf(contentType)
    if (draft.body === '') state.rows = normalizeParameters([])
    else if (state.boundary)
      state.rows = parseMultipart(draft.body, state.boundary)
    state.rawMultipart = !state.rows
  }
  return state
}

function patchBody(draft: Draft, state: BodyEditorState): Partial<Draft> {
  let contentType = state.contentType
  if (state.mode === 'none') contentType = undefined
  else if (contentType === undefined) {
    contentType =
      state.mode === 'multipart' && state.boundary
        ? multipartContentType(state.boundary)
        : CONTENT_TYPES[state.mode]
  }
  const headers = setContentType(draft.headers, contentType)
  return {
    body: state.body,
    headers,
    bodyEditor: { ...state, contentType },
  }
}

function freshSnapshot(
  mode: BodyMode,
  previous: BodyEditorState
): BodySnapshot {
  if (mode === 'none') return { body: '' }
  if (isRawBodyMode(mode))
    return {
      body: isRawBodyMode(previous.mode) ? previous.body : '',
      contentType: CONTENT_TYPES[mode],
    }
  const rows = previous.rows ?? normalizeParameters([])
  if (mode === 'urlencoded')
    return {
      rows,
      body: serializeParameters(rows),
      contentType: CONTENT_TYPES.urlencoded,
    }
  const { body, boundary } = multipartBody(rows)
  return {
    rows,
    body,
    boundary,
    contentType: multipartContentType(boundary),
  }
}

export function switchBodyMode(draft: Draft, mode: BodyMode): Partial<Draft> {
  const previous = bodyEditorOf(draft)
  if (previous.mode === mode) return {}
  const next = previous.saved[mode] ?? freshSnapshot(mode, previous)
  return patchBody(draft, {
    ...next,
    mode,
    saved: { ...previous.saved, [previous.mode]: snapshot(previous) },
    lastRawMode: isRawBodyMode(mode) ? mode : previous.lastRawMode,
  })
}

export function editBodyParameters(
  draft: Draft,
  rows: ParameterRow[]
): Partial<Draft> {
  const previous = bodyEditorOf(draft)
  const normalized = normalizeParameters(rows)
  if (previous.mode === 'multipart') {
    const { body, boundary } = multipartBody(normalized, previous.boundary)
    return patchBody(draft, {
      ...previous,
      rows: normalized,
      body,
      boundary,
      rawMultipart: false,
      contentType: multipartContentType(boundary),
    })
  }
  return patchBody(draft, {
    ...previous,
    rows: normalized,
    body: serializeParameters(normalized),
  })
}

export function editRawBody(draft: Draft, value: string): Partial<Draft> {
  if (draft.body === value) return {}
  return patchBody(draft, { ...bodyEditorOf(draft), body: value })
}
