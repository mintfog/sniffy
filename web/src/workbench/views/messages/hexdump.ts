/** 把 base64 二进制帧渲染成 hex dump 文本（offset | hex | ascii）。解码失败时原样返回。 */
export function hexDumpFromBase64(b64: string): string {
  let bytes: Uint8Array
  try {
    const bin = atob(b64)
    bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  } catch {
    return b64
  }
  const rows: string[] = []
  for (let off = 0; off < bytes.length; off += 16) {
    const slice = bytes.slice(off, off + 16)
    const hex = Array.from(slice).map((x) => x.toString(16).padStart(2, '0')).join(' ')
    const ascii = Array.from(slice).map((x) => (x >= 32 && x < 127 ? String.fromCharCode(x) : '.')).join('')
    rows.push(`${off.toString(16).padStart(8, '0')}  ${hex.padEnd(48, ' ')}  ${ascii}`)
  }
  return rows.join('\n')
}
