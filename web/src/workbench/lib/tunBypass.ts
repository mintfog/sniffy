/** 绕过列表的条目上限，与后端一致。 */
export const MAX_TUN_BYPASS = 256

/** 前缀长度下限：要比 TUN 指向虚拟网卡的 /1 路由更具体，与后端一致。 */
const MIN_BITS = 2

export type TunBypassProblem =
  | { kind: 'invalid'; value: string }
  | { kind: 'tooBroad'; value: string }
  | { kind: 'tooMany'; max: number }

export type TunBypassParse = { list: string[]; problem?: undefined } | { list?: undefined; problem: TunBypassProblem }

const IPV4_OCTET = '(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)'
const IPV4 = new RegExp(`^${IPV4_OCTET}(\\.${IPV4_OCTET}){3}$`)
// 规范化后的 IPv4 映射地址（::ffff:a.b.c.d），后端不接受。
const IPV4_MAPPED = /^\[::ffff:[0-9a-f]{1,4}:[0-9a-f]{1,4}\]$/
const BITS = /^(0|[1-9]\d{0,2})$/
const IPV6_CHARS = /^[0-9a-fA-F:.]+$/

function addressFamily(addr: string): 4 | 6 | undefined {
  if (IPV4.test(addr)) return 4
  // URL 的主机解析按 RFC 4291 校验 IPv6 的写法。先限定字符集：否则 ]、?、# 等会改变 URL 的结构，
  // 如 ::1]?x 会被拆成主机 [::1] 加查询串而通过；也挡掉了 zone（%eth0），它不是网段的一部分。
  if (!addr.includes(':') || !IPV6_CHARS.test(addr)) return undefined
  try {
    const host = new URL(`http://[${addr}]/`).hostname
    return IPV4_MAPPED.test(host) ? undefined : 6
  } catch {
    return undefined
  }
}

/**
 * 把输入框的内容拆成绕过列表：以换行、空格或逗号分隔，每项是 CIDR 或单个 IP。返回原样的各项
 * （主机位清零、去重由后端完成），或第一处问题。
 */
export function parseTunBypass(text: string): TunBypassParse {
  const items = [...new Set(text.split(/[\s,，]+/).filter(Boolean))]
  for (const value of items) {
    const slash = value.indexOf('/')
    const family = addressFamily(slash < 0 ? value : value.slice(0, slash))
    if (!family) return { problem: { kind: 'invalid', value } }
    if (slash < 0) continue
    const bitsText = value.slice(slash + 1)
    const bits = Number(bitsText)
    if (!BITS.test(bitsText) || bits > (family === 4 ? 32 : 128)) return { problem: { kind: 'invalid', value } }
    if (bits < MIN_BITS) return { problem: { kind: 'tooBroad', value } }
  }
  if (items.length > MAX_TUN_BYPASS) return { problem: { kind: 'tooMany', max: MAX_TUN_BYPASS } }
  return { list: items }
}
