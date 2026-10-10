export interface ParameterRow {
  id: string
  name: string
  value: string
  enabled: boolean
  /** 未编辑的字段沿用原文，避免改写导入表单的编码形式。 */
  original?: { name: string; value: string; raw: string }
}

let nextParameterId = 0

export function newParameterRow(name = '', value = ''): ParameterRow {
  nextParameterId += 1
  return { id: `param-${nextParameterId}`, name, value, enabled: true }
}

export function isBlankParameter(row: ParameterRow): boolean {
  return (
    row.name === '' &&
    row.value === '' &&
    (!row.original || row.original.name !== '' || row.original.value !== '')
  )
}

export function normalizeParameters(rows: ParameterRow[]): ParameterRow[] {
  let end = rows.length
  while (end > 0 && isBlankParameter(rows[end - 1])) end -= 1
  // 保留最靠前的尾部空行，让清空最后一项或连续输入时焦点仍落在原输入框。
  const blank = rows[end]
  const tail = blank
    ? { id: blank.id, name: '', value: '', enabled: true }
    : newParameterRow()
  return [...rows.slice(0, end), tail]
}

function decodeQueryPart(value: string): string {
  return new URLSearchParams(`value=${value}`).get('value') ?? ''
}

export function parseParameters(query: string): ParameterRow[] {
  if (!query) return [newParameterRow()]
  const rows = query.split('&').map(raw => {
    const equal = raw.indexOf('=')
    const name = decodeQueryPart(equal < 0 ? raw : raw.slice(0, equal))
    const value = equal < 0 ? '' : decodeQueryPart(raw.slice(equal + 1))
    return { ...newParameterRow(name, value), original: { name, value, raw } }
  })
  return normalizeParameters(rows)
}

export function serializeParameters(rows: ParameterRow[]): string {
  return rows
    .filter(row => row.enabled && !isBlankParameter(row))
    .map(row => {
      if (row.original?.name === row.name && row.original.value === row.value)
        return row.original.raw
      return new URLSearchParams([[row.name, row.value]]).toString()
    })
    .join('&')
}
