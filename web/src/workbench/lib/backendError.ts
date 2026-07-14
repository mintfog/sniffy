/**
 * 后端稳定错误码 → 本地化文案。
 *
 * Go 侧 internal/truststore 对已归类的安装失败以 "truststore:<code>[:<detail>]"
 * 返回稳定错误码(码表见该包 Code* 常量),词条在 certs.installErrors.<code> 下,
 * **改一处需同步另一处**。未知码或原始命令输出(证书工具 stderr 等)原样展示。
 */
import i18n from '@/i18n'

const CODED_ERROR = /^truststore:([a-z_]+)(?::([\s\S]+))?$/

/** 把安装根证书失败的错误文本转成当前语言文案;无法识别时返回原文。 */
export function localizeInstallError(raw: string): string {
  const m = CODED_ERROR.exec(raw)
  if (!m) return raw
  const key = `certs.installErrors.${m[1]}`
  if (!i18n.exists(key)) return raw
  return i18n.t(key, { detail: m[2] ?? '', seconds: m[2] ?? '' })
}
