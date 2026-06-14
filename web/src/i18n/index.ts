/**
 * 国际化（i18n）初始化。
 *
 * 选用 react-i18next + i18next-browser-languagedetector：语言的唯一真相源就是
 * i18next 自身，并由 languagedetector 缓存到 localStorage（key: sniffy-lang）。
 *   - 首次启动（localStorage 无缓存）回退到 navigator 语言 = 机器语言；
 *   - 用户在「设置」里切换后写入 localStorage，后续启动沿用其选择。
 * 不再单独在别处（zustand / 后端）保存语言，避免多一层同步。
 *
 * 资源（三套词条）直接 import 进来内联到 init，故初始化是同步的、首帧即就绪，
 * 不需要 Suspense、也不会有文案闪烁。
 *
 * 仅支持三种语言：英文 / 简体中文 / 繁体中文。任意 BCP-47 标签经 normalizeLang
 * 归一到这三者之一。
 */
import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import en from './locales/en.json'
import zhHans from './locales/zh-Hans.json'
import zhHant from './locales/zh-Hant.json'

export const SUPPORTED_LANGS = ['en', 'zh-Hans', 'zh-Hant'] as const
export type Lang = (typeof SUPPORTED_LANGS)[number]

/** 各语言用其自身书写的名称展示（语言选择器里恒用母语名，不随界面语言变化）。 */
export const LANG_LABELS: Record<Lang, string> = {
  en: 'English',
  'zh-Hans': '简体中文',
  'zh-Hant': '繁體中文',
}

/** localStorage 里缓存用户语言选择的键。 */
export const LANG_STORAGE_KEY = 'sniffy-lang'

/**
 * 把任意 BCP-47 语言标签归一到受支持的三种之一。
 *   - zh-Hant / zh-TW / zh-HK / zh-MO → 繁体中文
 *   - 其余 zh*（zh / zh-Hans / zh-CN / zh-SG）→ 简体中文
 *   - 其它一律英文
 */
export function normalizeLang(input?: string | null): Lang {
  const l = (input || '').toLowerCase()
  if (l.startsWith('zh')) {
    return /hant|tw|hk|mo/.test(l) ? 'zh-Hant' : 'zh-Hans'
  }
  return 'en'
}

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      en: { translation: en },
      'zh-Hans': { translation: zhHans },
      'zh-Hant': { translation: zhHant },
    },
    supportedLngs: SUPPORTED_LANGS as unknown as string[],
    fallbackLng: 'en',
    load: 'currentOnly',
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: LANG_STORAGE_KEY,
      caches: ['localStorage'],
      // 检测到的原始标签（zh-CN / en-US…）先归一，再用于匹配与缓存。
      convertDetectedLanguage: (lng: string) => normalizeLang(lng),
    },
    interpolation: { escapeValue: false }, // React 自带 XSS 转义
    returnNull: false,
  })

export default i18n
