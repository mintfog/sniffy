import { useCallback, useState } from 'react'
import { Shuffle, Wand2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAppStore } from '@/store'
import { Bridge } from '@/lib/bridge'
import type { TrafficRow } from '../lib/types'
import { openRulesWindow } from '../lib/windows'
import type { MenuNode } from '../ui/Menu'
import { ACTION_ICON } from './rules/actions'

/** Mock 正文写进 rules.json 的上限，与 Go 侧 service.maxMockBody 一致；超过时菜单项直接置灰。 */
const MAX_MOCK_BODY = 1 << 20

/** 与 Go 侧 service.FlowRule* 常量一一对应。 */
type RuleKind = 'mock' | 'block' | 'delay' | 'redirect' | 'setReqHeader' | 'custom'

/** Go 侧 service.CreateRuleFromFlow 返回的稳定错误代码，各有对应文案。 */
const RULE_ERROR_CODES = [
  'flow_not_found',
  'mock_flow_pending',
  'mock_has_trailers',
  'mock_body_too_large',
  'mock_body_binary',
  'mock_body_unavailable',
  'rule_target_binary',
]

export interface FlowRuleNotice {
  title: string
  message: string
}

/**
 * 流量行右键的「创建规则」子菜单：规则创建后直接打开规则窗口并选中它。
 */
export function useFlowRuleMenu() {
  const { t } = useTranslation()
  const [notice, setNotice] = useState<FlowRuleNotice | null>(null)

  const fail = useCallback(
    (message: string) => setNotice({ title: t('workbench.ctx.createFailed'), message }),
    [t],
  )

  const createRule = useCallback(
    async (row: TrafficRow, kind: RuleKind) => {
      const target = `${row.method} ${row.host}${row.path.split('?')[0]}`
      let created
      try {
        created = await Bridge.createRuleFromFlow(row.id, kind, t(`workbench.ctx.ruleName.${kind}`, { target }))
      } catch (e) {
        const code = e instanceof Error ? e.message : String(e)
        fail(RULE_ERROR_CODES.includes(code) ? t(`workbench.ctx.ruleError.${code}`) : t('workbench.ctx.ruleFailed'))
        return
      }
      await openRulesWindow(created.id).catch(() => {})
    },
    [fail, t],
  )

  const menuFor = useCallback(
    (row: TrafficRow): MenuNode[] => {
      if (row.kind !== 'http') return []
      const mockable =
        row.state !== 'pending' &&
        !row.paused &&
        row.status !== undefined &&
        (row.sizeBytes ?? 0) <= MAX_MOCK_BODY &&
        // 读快照而不订阅：订阅会让工作台随每条流消息重渲染。
        !useAppStore.getState().streamSessions.some((s) => s.id === row.id)
      const ruleItem = (kind: RuleKind, disabled = false): MenuNode => ({
        label: t(`workbench.ctx.rule.${kind}`),
        icon: kind === 'custom' ? Wand2 : ACTION_ICON[kind],
        disabled,
        onSelect: () => void createRule(row, kind),
      })
      return [
        {
          label: t('workbench.ctx.createRule'),
          icon: Shuffle,
          submenu: [
            { type: 'label', label: t('workbench.ctx.ruleGroup.ready') },
            ruleItem('mock', !mockable),
            ruleItem('block'),
            ruleItem('delay'),
            { type: 'separator' },
            { type: 'label', label: t('workbench.ctx.ruleGroup.draft') },
            ruleItem('redirect'),
            ruleItem('setReqHeader'),
            ruleItem('custom'),
          ],
        },
      ]
    },
    [createRule, t],
  )

  const clearNotice = useCallback(() => setNotice(null), [])

  return { menuFor, notice, clearNotice }
}
