import i18n from '@/i18n'

export interface LogEntry {
  level: string
  msg: string
  time: number
}

/** 枚举配置项的候选；value 可为任意标量。 */
export interface SettingOption {
  value: unknown
  label?: string
}

/** 配置项声明，驱动配置页按类型渲染表单。与后端 plugin.SettingField 对应。 */
export interface SettingField {
  key: string
  label?: string
  type?: 'string' | 'text' | 'number' | 'boolean' | 'enum'
  description?: string
  default?: unknown
  placeholder?: string
  options?: SettingOption[]
}

export interface Plugin {
  id: string
  name: string
  version: string
  description: string
  enabled: boolean
  author?: string
  runtime?: string
  priority?: number
  whitelist?: string[]
  blacklist?: string[]
  settings?: Record<string, unknown>
  settingsSchema?: SettingField[]
  error?: string
}

/** 单插件保留的实时日志条数上限（超出从头丢弃）。 */
export const LOG_CAP = 500

export const LOG_TONE: Record<string, string> = {
  error: 'text-danger',
  warn: 'text-warn',
  notify: 'text-iris',
  info: 'text-fg',
  log: 'text-fg-muted',
  debug: 'text-fg-faint',
}

function asStringArray(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
}

export function toPlugin(m: Record<string, unknown>): Plugin {
  return {
    id: String(m.id ?? ''),
    name: String(m.name ?? m.id ?? i18n.t('plugins.unnamed')),
    version: String(m.version ?? ''),
    description: String(m.description ?? ''),
    enabled: Boolean(m.enabled),
    author: m.author ? String(m.author) : undefined,
    runtime: m.runtime ? String(m.runtime) : undefined,
    priority: typeof m.priority === 'number' ? (m.priority as number) : undefined,
    whitelist: asStringArray(m.whitelist),
    blacklist: asStringArray(m.blacklist),
    settings: (m.settings as Record<string, unknown>) ?? undefined,
    settingsSchema: Array.isArray(m.settingsSchema) ? (m.settingsSchema as SettingField[]) : undefined,
    error: m.error ? String(m.error) : undefined,
  }
}

export function toLogs(v: unknown): LogEntry[] {
  if (!Array.isArray(v)) return []
  return v.map((e) => {
    const o = e as Record<string, unknown>
    return { level: String(o.level ?? 'log'), msg: String(o.msg ?? ''), time: Number(o.time ?? 0) }
  })
}
