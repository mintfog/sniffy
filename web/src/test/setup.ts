import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import { afterEach, beforeEach, vi } from 'vitest'
import zh from '../i18n/locales/zh-Hans.json'
import en from '../i18n/locales/en.json'

await i18n.use(initReactI18next).init({
  lng: 'zh-Hans',
  fallbackLng: 'zh-Hans',
  resources: { 'zh-Hans': { translation: zh }, en: { translation: en } },
  interpolation: { escapeValue: false },
})

beforeEach(async () => {
  await i18n.changeLanguage('zh-Hans')
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
})
