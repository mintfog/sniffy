/**
 * 语言桥接：跨 Wails 窗口同步语言切换 + 同步 <html lang> 与文档标题。
 *
 * 桌面端是多窗口的（设置/工具箱/关于会弹独立系统窗）。在任一窗口切换语言时，经 Wails
 * 事件总线广播给其它窗口，使全部窗口界面语言一致——与 prefs.ts 的跨窗口外观同步同理。
 */
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Events } from '@wailsio/runtime'
import i18n, { normalizeLang, type Lang } from './index'

const LANG_EVENT = 'lang_changed'

/** 切换界面语言：本窗口切换 + 广播给其它窗口。语言选择器（设置页）应调用此函数。 */
export function changeLang(lang: Lang): void {
  void i18n.changeLanguage(lang)
  try {
    void Events.Emit(LANG_EVENT, lang)
  } catch {
    /* 非 Wails 环境（浏览器预览）：忽略 */
  }
}

/**
 * 在应用根挂载一次：让 <html lang> 与文档标题随语言更新，并接收其它窗口的语言变更。
 */
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
        const next = Array.isArray(e?.data) ? e.data[0] : e?.data
        if (typeof next !== 'string') return
        const n = normalizeLang(next)
        if (n !== i18n.language) void i18n.changeLanguage(n)
      })
    } catch {
      /* 非 Wails 环境：忽略 */
    }
    return () => {
      try {
        off()
      } catch {
        /* ignore */
      }
    }
  }, [])
}
