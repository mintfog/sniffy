import { useCallback, useEffect, useState } from 'react'
import { CircleDot, Shuffle, Wand2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAppStore } from '@/store'
import { Bridge, type BreakRule } from '@/lib/bridge'
import {
  breakChecked,
  breakTarget,
  MAX_MOCK_BODY,
  toggleBreak,
  type BreakPhase,
  type BreakScope,
  type BreakTarget,
} from '../lib/fromFlow'
import type { TrafficRow } from '../lib/types'
import { openRulesWindow } from '../lib/windows'
import type { MenuNode } from '../ui/Menu'
import { ACTION_ICON } from './rules/actions'

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

/** 流量行右键的「断点」「创建规则」子菜单；menuOpen 变为真时拉取断点规则用于勾选态。 */
export function useFlowRuleMenu(menuOpen: boolean) {
  const { t } = useTranslation()
  // null 表示尚未加载完，此时断点项置灰。
  const [breakRules, setBreakRules] = useState<BreakRule[] | null>(null)
  const [notice, setNotice] = useState<FlowRuleNotice | null>(null)

  useEffect(() => {
    if (!menuOpen) return
    let alive = true
    setBreakRules(null)
    Bridge.getBreakRules()
      .then((rs) => {
        if (alive) setBreakRules(rs ?? [])
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [menuOpen])

  const fail = useCallback(
    (message: string) => setNotice({ title: t('workbench.ctx.createFailed'), message }),
    [t],
  )

  const toggleBreakRule = useCallback(
    async (target: BreakTarget, phase: BreakPhase) => {
      if (!breakRules) return
      try {
        for (const op of toggleBreak(breakRules, target, phase)) {
          if (op.kind === 'add') {
            const created = await Bridge.addBreakRule(op.target.url, op.onRequest, op.onResponse, op.target.match)
            setBreakRules((rs) => (rs && created ? [...rs, created] : rs))
          } else if (op.kind === 'update') {
            const r = op.rule
            // false 表示规则已在别处被删。
            if (!(await Bridge.updateBreakRule(r.id, r.url, r.onRequest, r.onResponse, r.enabled))) throw new Error('gone')
            setBreakRules((rs) => rs && rs.map((x) => (x.id === r.id ? r : x)))
          } else {
            await Bridge.deleteBreakRule(op.id)
            setBreakRules((rs) => rs && rs.filter((x) => x.id !== op.id))
          }
        }
      } catch {
        fail(t('workbench.ctx.breakFailed'))
      }
    },
    [breakRules, fail, t],
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
      const breakItems = (scope: BreakScope): MenuNode[] => {
        const target = breakTarget(row.url, scope)
        return [
          { type: 'label', label: t(`workbench.ctx.breakScope.${scope}`) },
          ...(['request', 'response'] as const).map((phase) => ({
            label: t(`workbench.ctx.breakPhase.${phase}`),
            checked: !!breakRules && breakChecked(breakRules, target, phase),
            disabled: !breakRules,
            onSelect: () => void toggleBreakRule(target, phase),
          })),
        ]
      }
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
          label: t('workbench.ctx.breakpoint'),
          icon: CircleDot,
          submenu: [...breakItems('path'), { type: 'separator' }, ...breakItems('host')],
        },
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
    [breakRules, toggleBreakRule, createRule, t],
  )

  const clearNotice = useCallback(() => setNotice(null), [])

  return { menuFor, notice, clearNotice }
}
