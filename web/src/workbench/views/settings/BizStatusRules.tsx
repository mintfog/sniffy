import { useTranslation } from 'react-i18next'
import { Plus, Trash2 } from 'lucide-react'
import { usePrefs } from '../../prefs'
import { Button, TextInput } from '../../ui/controls'
import { EMPTY_BIZ_RULE, type BizRuleDraft } from '../../lib/bizStatus'

const COLUMNS: { key: keyof BizRuleDraft; placeholder: string }[] = [
  { key: 'host', placeholder: '*.example.com' },
  { key: 'fields', placeholder: 'code, errcode' },
  { key: 'success', placeholder: '0, 200' },
  { key: 'messageField', placeholder: 'msg' },
]

export function BizStatusRules() {
  const { t } = useTranslation()
  const rules = usePrefs((s) => s.bizStatusRules)
  const set = usePrefs((s) => s.set)

  const update = (index: number, patch: Partial<BizRuleDraft>) =>
    set({ bizStatusRules: rules.map((r, i) => (i === index ? { ...r, ...patch } : r)) })

  return (
    <div className="settings-biz-rules">
      <div className="settings-field-hint">{t('settings.bizStatus.hint')}</div>
      {rules.length > 0 && (
        <div className="settings-biz-grid" role="table" aria-label={t('settings.sections.bizStatus')}>
          <div className="settings-biz-row settings-biz-head" role="row">
            {COLUMNS.map((c) => (
              <span key={c.key} role="columnheader">
                {t(`settings.bizStatus.${c.key}`)}
              </span>
            ))}
            <span />
          </div>
          {rules.map((rule, i) => (
            <div key={i} className="settings-biz-row" role="row">
              {COLUMNS.map((c) => (
                <TextInput
                  key={c.key}
                  aria-label={`${t(`settings.bizStatus.${c.key}`)} ${i + 1}`}
                  className="min-w-0 font-mono"
                  value={rule[c.key]}
                  placeholder={c.placeholder}
                  onChange={(e) => update(i, { [c.key]: e.target.value })}
                />
              ))}
              <Button
                variant="ghost"
                aria-label={`${t('settings.bizStatus.remove')} ${i + 1}`}
                title={t('settings.bizStatus.remove')}
                icon={<Trash2 className="h-3.5 w-3.5" />}
                onClick={() => set({ bizStatusRules: rules.filter((_, j) => j !== i) })}
              />
            </div>
          ))}
        </div>
      )}
      <Button
        size="sm"
        icon={<Plus className="h-3.5 w-3.5" />}
        onClick={() => set({ bizStatusRules: [...rules, { ...EMPTY_BIZ_RULE }] })}
      >
        {t('settings.bizStatus.add')}
      </Button>
    </div>
  )
}
