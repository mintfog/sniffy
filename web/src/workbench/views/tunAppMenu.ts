import { useCallback, useState } from 'react'
import { AppWindow } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Bridge, type TunAppChange } from '@/lib/bridge'
import { isTunOn, tunPhaseOf, useTun } from '../lib/tun'
import type { TrafficRow } from '../lib/types'
import type { MenuNode } from '../ui/Menu'

export interface TunAppNotice {
  tone: 'success' | 'error'
  title: string
  message: string
}

/** 结果里最多列出的应用数，再多的只报总数。 */
const LISTED_APPS = 5

/** 流量行右键的「TUN 模式」子菜单：只抓、不抓这条会话所属的应用，改完后给出说明。 */
export function useTunAppMenu() {
  const { t } = useTranslation()
  const [notice, setNotice] = useState<TunAppNotice | null>(null)

  const describe = useCallback(
    ({ app, config }: TunAppChange, capture: boolean): TunAppNotice => {
      const include = config.tunAppMode === 'include'
      const apps = (include ? config.tunIncludeApps : config.tunExcludeApps) ?? []
      const listed = apps.slice(0, LISTED_APPS).join(t('workbench.ctx.tunApp.separator'))
      const list = apps.length > LISTED_APPS ? t('workbench.ctx.tunApp.more', { list: listed, count: apps.length }) : listed
      const stateKey = !include ? 'exceptList' : apps.length > 0 ? 'onlyList' : 'nothing'
      const { status, stopPending } = useTun.getState()
      const later = isTunOn(tunPhaseOf(status, stopPending)) ? '' : t('workbench.ctx.tunApp.whenOn')
      const titleKey = capture ? 'captured' : include ? 'removed' : 'skipped'
      return {
        tone: 'success',
        title: t(`workbench.ctx.tunApp.${titleKey}`, { app }),
        message: [t(`workbench.ctx.tunApp.${stateKey}`, { list }), later, t('workbench.ctx.tunApp.where')].filter(Boolean).join('\n'),
      }
    },
    [t],
  )

  const apply = useCallback(
    async (row: TrafficRow, capture: boolean) => {
      try {
        setNotice(describe(await Bridge.setTunAppCaptured(row.processPath ?? '', row.process ?? '', capture), capture))
      } catch (e) {
        setNotice({ tone: 'error', title: t('workbench.ctx.tunApp.failed'), message: e instanceof Error ? e.message : String(e) })
      }
    },
    [describe, t],
  )

  const menuFor = useCallback(
    (row: TrafficRow): MenuNode[] => {
      if (!row.process && !row.processPath) return []
      return [
        {
          label: t('workbench.ctx.tunApp.title'),
          icon: AppWindow,
          submenu: [
            { label: t('workbench.ctx.tunApp.capture'), onSelect: () => void apply(row, true) },
            { label: t('workbench.ctx.tunApp.skip'), onSelect: () => void apply(row, false) },
          ],
        },
        { type: 'separator' },
      ]
    },
    [apply, t],
  )

  const clearNotice = useCallback(() => setNotice(null), [])

  return { menuFor, notice, clearNotice }
}
