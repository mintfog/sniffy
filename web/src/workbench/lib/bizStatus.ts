/** 业务状态码提取规则：设置页编辑态与后端配置之间的转换。 */

/** 后端配置（Go 侧 service.BizStatusRule）。 */
export interface BizStatusRule {
  host: string
  fields: string[]
  success?: string[]
  messageField?: string
}

/** 设置页编辑态：列表项以逗号分隔的原始文本保存，保留输入中的状态。 */
export interface BizRuleDraft {
  host: string
  fields: string
  success: string
  messageField: string
}

export const EMPTY_BIZ_RULE: BizRuleDraft = { host: '', fields: '', success: '', messageField: '' }

function splitList(s: string): string[] {
  return s
    .split(/[,，]/)
    .map((x) => x.trim())
    .filter(Boolean)
}

/** 与后端 hostglob.Clean 一致：用户常直接粘贴 URL，只保留主机（含端口）。 */
export function cleanHost(s: string): string {
  let h = s.trim().toLowerCase()
  const i = h.indexOf('://')
  if (i >= 0) h = h.slice(i + 3)
  return h.split(/[/?#]/, 1)[0]
}

/** 只下发已填好主机与字段的规则，编辑到一半的行留在本地。 */
export function toBizStatusRules(drafts: readonly BizRuleDraft[]): BizStatusRule[] {
  const out: BizStatusRule[] = []
  for (const d of drafts) {
    const host = cleanHost(d.host)
    const fields = splitList(d.fields)
    if (!host || fields.length === 0) continue
    const rule: BizStatusRule = { host, fields }
    const success = splitList(d.success)
    if (success.length) rule.success = success
    const messageField = d.messageField.trim()
    if (messageField) rule.messageField = messageField
    out.push(rule)
  }
  return out
}

export function fromBizStatusRules(rules: readonly BizStatusRule[]): BizRuleDraft[] {
  return rules.map((r) => ({
    host: r.host,
    fields: r.fields.join(', '),
    success: (r.success ?? []).join(', '),
    messageField: r.messageField ?? '',
  }))
}

export function hasBizRules(drafts: readonly BizRuleDraft[]): boolean {
  return toBizStatusRules(drafts).length > 0
}
