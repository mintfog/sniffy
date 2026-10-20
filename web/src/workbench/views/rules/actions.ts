import type { TFunction } from 'i18next'
import {
  ArrowRightLeft,
  Ban,
  Clock,
  FileText,
  Link2,
  Replace,
  type LucideIcon,
  Wand2,
  Zap,
} from 'lucide-react'
import type { ActionType } from '../../lib/rulesMap'

// 声明顺序决定动作选择器的展示顺序。
export const ACTION_ICON: Record<ActionType, LucideIcon> = {
  redirect: ArrowRightLeft,
  rewriteUrl: Link2,
  setReqHeader: FileText,
  replaceReqBody: Replace,
  mock: Zap,
  setResBody: Wand2,
  delay: Clock,
  block: Ban,
}

export function actionParamMeta(
  type: Exclude<ActionType, 'block'>,
  t: TFunction
): {
  paramLabel: string
  paramPlaceholder: string
  extraLabel?: string
  extraPlaceholder?: string
  extraMultiline?: boolean
} {
  switch (type) {
    case 'redirect':
      return {
        paramLabel: t('rules.param.targetAddr'),
        paramPlaceholder: 'http://127.0.0.1:3000',
      }
    case 'rewriteUrl':
      return {
        paramLabel: t('rules.param.matchRegex'),
        paramPlaceholder: '^https://cdn\\.x\\.com',
        extraLabel: t('rules.param.replaceWith'),
        extraPlaceholder: 'https://cdn.local',
      }
    case 'setReqHeader':
      return {
        paramLabel: t('rules.param.headerName'),
        paramPlaceholder: 'Authorization',
        extraLabel: t('rules.param.headerValue'),
        extraPlaceholder: 'Bearer …',
      }
    case 'replaceReqBody':
      return {
        paramLabel: 'Content-Type',
        paramPlaceholder: t('rules.param.contentTypeOptional'),
        extraLabel: t('rules.param.requestBody'),
        extraPlaceholder: t('rules.param.requestBodyPlaceholder'),
        extraMultiline: true,
      }
    case 'setResBody':
      return {
        paramLabel: t('rules.param.matchText'),
        paramPlaceholder: '"env":"prod"',
        extraLabel: t('rules.param.replaceWith'),
        extraPlaceholder: '"env":"dev"',
      }
    case 'mock':
      return {
        paramLabel: t('rules.param.statusCode'),
        paramPlaceholder: '200',
        extraLabel: t('rules.param.responseBodyJson'),
        extraPlaceholder: '{ }',
        extraMultiline: true,
      }
    case 'delay':
      return { paramLabel: t('rules.param.delayMs'), paramPlaceholder: '1000' }
  }
}
