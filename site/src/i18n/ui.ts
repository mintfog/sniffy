import type { Lang, SiteCopy } from "./types";
import en from "./locales/en.json";
import zh from "./locales/zh.json";

export { languages, localePath, otherLang } from "./types";
export type {
  Lang,
  HomeCopy,
  Paragraph,
  SiteCopy,
  WorkbenchCopy,
} from "./types";

// 显式标注文案结构，让缺失的翻译字段在 astro check 时报告。
export const siteCopy: Record<Lang, SiteCopy> = { zh, en };
