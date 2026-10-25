import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AppWindow,
  ArrowRight,
  Check,
  Database,
  Info,
  Network,
  Palette,
  Search,
  ShieldCheck,
  X,
} from 'lucide-react'
import { detectPlatform } from '@/lib/platform'
import './settings.css'

const CATEGORIES = [
  { id: 'general', icon: AppWindow },
  { id: 'proxy', icon: Network },
  { id: 'decrypt', icon: ShieldCheck },
  { id: 'storage', icon: Database },
  { id: 'appearance', icon: Palette },
  { id: 'about', icon: Info },
] as const

type Category = (typeof CATEGORIES)[number]['id']

export interface SettingsSection {
  id: string
  category: Category
  title: string
  description?: string
  searchKeys: string[]
  content: ReactNode
}

export function SettingsField({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className="settings-field" role="group" aria-label={label}>
      <div className="settings-field-copy">
        <div className="settings-field-label">{label}</div>
        {hint && <div className="settings-field-hint">{hint}</div>}
      </div>
      <div className="settings-field-control">{children}</div>
    </div>
  )
}

export function SettingsLayout({
  sections,
  children,
}: {
  sections: SettingsSection[]
  children: ReactNode
}) {
  const { t } = useTranslation()
  const [category, setCategory] = useState<Category>('proxy')
  const [query, setQuery] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  const searching = terms.length > 0
  const visibleSections = sections.filter(section => {
    if (!searching) return section.category === category
    const text = [
      t(`settings.${section.category}.title`),
      section.title,
      section.description,
      ...section.searchKeys.map(key => t(`settings.${key}`)),
    ]
      .join(' ')
      .toLocaleLowerCase()
    return terms.every(term => text.includes(term))
  })

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        !(event.ctrlKey || event.metaKey) ||
        event.altKey ||
        event.shiftKey ||
        event.key.toLowerCase() !== 'k'
      )
        return
      if (document.querySelector('[aria-modal="true"]')) return
      event.preventDefault()
      event.stopImmediatePropagation()
      searchRef.current?.focus()
      searchRef.current?.select()
    }
    window.addEventListener('keydown', onKeyDown, true)
    return () => window.removeEventListener('keydown', onKeyDown, true)
  }, [])

  useEffect(() => {
    if (contentRef.current) contentRef.current.scrollTop = 0
  }, [category, query])

  const selectCategory = (next: Category) => {
    setCategory(next)
    setQuery('')
  }

  return (
    <div className="settings-page">
      <header className="settings-header">
        <div className="settings-heading">
          <h1>{t('settings.title')}</h1>
          <span className="settings-save-hint">
            <Check aria-hidden="true" />
            {t('settings.autoSave')}
          </span>
        </div>
        <div className="settings-search" role="search">
          <Search aria-hidden="true" />
          <input
            ref={searchRef}
            type="search"
            value={query}
            onChange={event => setQuery(event.target.value)}
            onKeyDown={event => {
              if (event.key !== 'Escape' || event.nativeEvent.isComposing)
                return
              event.preventDefault()
              event.stopPropagation()
              setQuery('')
            }}
            aria-label={t('settings.search.label')}
            placeholder={t('settings.search.placeholder')}
            spellCheck={false}
          />
          {query ? (
            <button
              type="button"
              aria-label={t('settings.search.clear')}
              onClick={() => {
                setQuery('')
                searchRef.current?.focus()
              }}
            >
              <X aria-hidden="true" />
            </button>
          ) : (
            <kbd aria-hidden="true">
              {detectPlatform() === 'mac' ? '⌘ K' : 'Ctrl K'}
            </kbd>
          )}
        </div>
      </header>
      <div className="settings-body">
        <nav className="settings-nav" aria-label={t('settings.categories')}>
          {CATEGORIES.map(({ id, icon: Icon }) => (
            <button
              key={id}
              type="button"
              aria-current={!searching && category === id ? 'page' : undefined}
              onClick={() => selectCategory(id)}
            >
              <Icon aria-hidden="true" />
              <span>{t(`settings.${id}.title`)}</span>
            </button>
          ))}
        </nav>
        <div
          ref={contentRef}
          className="settings-content"
          role="region"
          aria-label={t('settings.title')}
          tabIndex={0}
        >
          <div className="settings-content-inner">
            <div className="settings-content-heading">
              <h2>
                {searching
                  ? t('settings.search.results')
                  : t(`settings.${category}.title`)}
              </h2>
              {searching && (
                <span role="status">
                  {t('settings.search.count', {
                    count: visibleSections.length,
                  })}
                </span>
              )}
            </div>
            {visibleSections.map(section => (
              <section
                key={section.id}
                className="settings-section"
                aria-labelledby={`settings-${section.id}`}
              >
                <div className="settings-section-heading">
                  <h3 id={`settings-${section.id}`}>
                    {searching && (
                      <span className="settings-breadcrumb">
                        {t(`settings.${section.category}.title`)} /{' '}
                      </span>
                    )}
                    {section.title}
                  </h3>
                  {section.description && <p>{section.description}</p>}
                </div>
                <div className="settings-card">{section.content}</div>
              </section>
            ))}
            {searching && visibleSections.length === 0 && (
              <div className="settings-empty">
                <Search aria-hidden="true" />
                <h3>{t('settings.search.empty')}</h3>
                <p>{t('settings.search.emptyHint')}</p>
                <button
                  type="button"
                  onClick={() => {
                    setQuery('')
                    searchRef.current?.focus()
                  }}
                >
                  {t('settings.search.clear')}
                </button>
              </div>
            )}
            {!searching && category === 'proxy' && (
              <div className="settings-related">
                <ShieldCheck aria-hidden="true" />
                <span>{t('settings.proxy.decryptHint')}</span>
                <button type="button" onClick={() => selectCategory('decrypt')}>
                  {t('settings.proxy.openDecrypt')}
                  <ArrowRight aria-hidden="true" />
                </button>
              </div>
            )}
          </div>
        </div>
      </div>
      {children}
    </div>
  )
}
