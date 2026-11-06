import React, { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check } from 'lucide-react'
import { usePrefs } from '../../prefs'
import {
  derivePalette,
  PRESET_KEYS,
  PRESET_PALETTES,
  SEED_KEYS,
  type CustomPalette,
  type PaletteKey,
  type PaletteSeeds,
  type PresetPalette,
  type ThemeMode,
} from '../../theme/palette'
import { Select, SegTabs } from '../../ui/controls'
import { cx } from '../../ui/primitives'

/** 色板缩略：chrome 底 + 内容面 + 主色胶囊，按当前明暗展示该方案的观感 */
function Swatch({ seeds }: { seeds: PaletteSeeds }) {
  return (
    <span
      aria-hidden
      className="flex h-9 w-full overflow-hidden rounded-wb border"
      style={{ borderColor: seeds.border, background: seeds.sidebar }}
    >
      <span className="w-3 shrink-0" style={{ background: seeds.sidebar, borderRight: `1px solid ${seeds.border}` }} />
      <span className="flex flex-1 flex-col justify-center gap-1 px-1.5" style={{ background: seeds.bg }}>
        <span className="h-1 w-3/4 rounded-full" style={{ background: seeds.fg, opacity: 0.85 }} />
        <span className="h-1 w-1/2 rounded-full" style={{ background: seeds.fg, opacity: 0.4 }} />
      </span>
      <span className="flex w-5 shrink-0 items-center justify-center" style={{ background: seeds.bg }}>
        <span className="h-2.5 w-2.5 rounded-full" style={{ background: seeds.primary }} />
      </span>
    </span>
  )
}

export function PaletteChooser({ mode }: { mode: ThemeMode }) {
  const { t } = useTranslation()
  const palette = usePrefs((s) => s.palette)
  const custom = usePrefs((s) => s.customPalette)
  const set = usePrefs((s) => s.set)
  const keys: PaletteKey[] = [...PRESET_KEYS, 'custom']

  return (
    <div className="grid grid-cols-4 gap-2" role="radiogroup" aria-label={t('settings.appearance.palette')}>
      {keys.map((key) => {
        const name = t(`settings.appearance.paletteNames.${key}`)
        const seeds = key === 'custom' ? custom[mode] : PRESET_PALETTES[key][mode]
        const active = palette === key
        return (
          <button
            key={key}
            type="button"
            role="radio"
            aria-checked={active}
            aria-label={t('settings.appearance.paletteSwatch', { name })}
            onClick={() => set({ palette: key })}
            className={cx(
              'flex w-[88px] flex-col gap-1 rounded-control border p-1 text-left outline-none transition-colors focus-visible:shadow-focus',
              active ? 'border-fg' : 'border-line hover:border-line-strong hover:bg-hover',
            )}
          >
            <Swatch seeds={seeds} />
            <span className="flex items-center gap-1 px-0.5 text-[12px] leading-4">
              <span className={cx('truncate', active ? 'text-fg' : 'text-fg-muted')}>{name}</span>
              {active && <Check className="ml-auto h-3 w-3 shrink-0 text-fg" />}
            </span>
          </button>
        )
      })}
    </div>
  )
}

/** 预览只在局部容器写入推导变量，编辑另一套明暗时不影响当前界面 */
function PalettePreview({ vars }: { vars: Record<string, string> }) {
  const { t } = useTranslation()
  return (
    <div
      className="overflow-hidden rounded-wb border border-line bg-base text-fg"
      style={vars as React.CSSProperties}
      aria-label={t('settings.appearance.preview')}
    >
      <div className="flex">
        <div className="w-16 shrink-0 border-r border-line bg-inset" />
        <div className="min-w-0 flex-1 bg-surface">
          <div className="flex h-7 items-center border-b border-line bg-inset px-2 text-[12px] font-medium text-fg-muted">
            {t('settings.appearance.previewSample.title')}
          </div>
          <div className="flex h-row items-center gap-2 px-2 font-mono text-[12px]">
            <span className="text-2xs font-semibold text-method-get">GET</span>
            <span className="text-ok">200</span>
            <span className="truncate">api.example.com/v1/users</span>
          </div>
          <div className="flex h-row items-center gap-2 bg-row-selected px-2 font-mono text-[12px]">
            <span className="text-2xs font-semibold text-method-post">POST</span>
            <span className="text-info">404</span>
            <span className="truncate">{t('settings.appearance.previewSample.selected')}</span>
          </div>
          <div className="flex h-row items-center gap-2 px-2 font-mono text-[12px]">
            <span className="text-2xs font-semibold text-method-delete">DELETE</span>
            <span className="text-danger">500</span>
            <span className="truncate text-fg-muted">{t('settings.appearance.previewSample.muted')}</span>
          </div>
          <div className="flex items-center justify-end gap-2 border-t border-line px-2 py-1.5">
            <span className="inline-flex h-control-xs items-center rounded-control bg-sel px-2.5 text-[12px] font-medium text-sel-fg">
              {t('settings.appearance.previewSample.primary')}
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}

export function CustomPaletteEditor({ mode: themeMode }: { mode: ThemeMode }) {
  const { t } = useTranslation()
  const custom = usePrefs((s) => s.customPalette)
  const set = usePrefs((s) => s.set)
  const [mode, setMode] = useState<ThemeMode>(themeMode)
  const seeds = custom[mode]
  const derived = useMemo(() => derivePalette(seeds, mode), [seeds, mode])

  const update = (next: CustomPalette) => set({ palette: 'custom', customPalette: next })
  const setSeed = (key: keyof PaletteSeeds, value: string) =>
    update({ ...custom, [mode]: { ...seeds, [key]: value.toUpperCase() } })

  return (
    <div className="flex flex-col gap-3 px-[18px] pb-4 pt-1">
      <div className="flex items-center justify-between gap-3">
        <SegTabs<ThemeMode>
          value={mode}
          onChange={setMode}
          options={[
            { key: 'light', label: t('settings.appearance.themeLight') },
            { key: 'dark', label: t('settings.appearance.themeDark') },
          ]}
        />
        <Select
          aria-label={t('settings.appearance.copyFromPreset')}
          value=""
          onChange={(e) => {
            const key = e.target.value as PresetPalette
            if (key) update({ ...custom, [mode]: { ...PRESET_PALETTES[key][mode] } })
          }}
          options={[
            { value: '', label: t('settings.appearance.copyFromPreset') },
            ...PRESET_KEYS.map((k) => ({ value: k, label: t(`settings.appearance.paletteNames.${k}`) })),
          ]}
        />
      </div>
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)] gap-4">
        <div className="flex flex-col gap-1.5">
          {SEED_KEYS.map((key) => {
            const effective = derived.adjusted[key]
            return (
              <label key={key} className="flex min-h-control-sm items-center gap-2.5">
                <span className="relative h-5 w-5 shrink-0 overflow-hidden rounded-wb-sm border border-line-strong">
                  <span className="absolute inset-0" style={{ background: seeds[key] }} />
                  <input
                    type="color"
                    value={seeds[key]}
                    onChange={(e) => setSeed(key, e.target.value)}
                    className="absolute inset-0 h-full w-full cursor-pointer opacity-0"
                    aria-label={t(`settings.appearance.seeds.${key}`)}
                  />
                </span>
                <span className="w-14 shrink-0 text-[12px] text-fg">{t(`settings.appearance.seeds.${key}`)}</span>
                <span className="font-mono text-[12px] text-fg-muted">{seeds[key]}</span>
                {effective && (
                  <span className="flex min-w-0 items-center gap-1 text-2xs text-warn">
                    <span className="h-2.5 w-2.5 shrink-0 rounded-full border border-line" style={{ background: effective }} />
                    <span className="truncate">{t('settings.appearance.seedAdjusted', { color: effective })}</span>
                  </span>
                )}
              </label>
            )
          })}
        </div>
        <PalettePreview vars={derived.vars} />
      </div>
    </div>
  )
}
