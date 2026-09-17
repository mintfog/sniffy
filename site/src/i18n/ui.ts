import type { Lang, SiteCopy } from "./types";
import en from "./locales/en.json";
import zh from "./locales/zh.json";

export { languages, localePath, otherLang } from "./types";
export type { Lang, HomeCopy, Paragraph, SiteCopy, WorkbenchCopy } from "./types";

/**
 * 新增语言：在 locales/ 下加一份 JSON，扩展 types.ts 的 Lang，再在此登记。
 * 这里的类型标注是漏译的唯一防线——缺字段会在 astro check 报错。
 */
const copy: Record<Lang, SiteCopy> = { zh, en };

export function siteCopy(lang: Lang): SiteCopy {
  return copy[lang];
}
