import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Events } from '@wailsio/runtime'
import i18n, { normalizeLang, type Lang } from './index'

const LANG_EVENT = 'lang_changed'

/** 语言选择器通过此入口切换语言，并广播给其他窗口及原生托盘。 */
export function changeLang(lang: Lang): void {
  void i18n.changeLanguage(lang)
  try {
    void Events.Emit(LANG_EVENT, lang)
  } catch {
    /* 非 Wails 环境（浏览器预览）：忽略 */
  }
}

/** 在每个窗口的应用根挂载一次，同步语言、文档标题及 html lang。 */
export function useLangBridge(): void {
  const { i18n: inst, t } = useTranslation()
  const lang = inst.language

  useEffect(() => {
    document.documentElement.lang = lang
    document.title = t('app.documentTitle')
  }, [lang, t])

  useEffect(() => {
    let off = () => {}
    try {
      off = Events.On(LANG_EVENT, (e: { data?: unknown }) => {
        const next = e.data
        if (typeof next !== 'string') return
        const nextLang = normalizeLang(next)
        if (nextLang !== i18n.language) void i18n.changeLanguage(nextLang)
      })
      // 首次挂载时将原生托盘菜单同步到用户选择的语言。
      void Events.Emit(LANG_EVENT, i18n.language)
    } catch {
      /* 非 Wails 环境：忽略 */
    }
    return off
  }, [])
}
