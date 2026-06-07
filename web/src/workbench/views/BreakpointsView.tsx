import { useState } from 'react'
import { ArrowDownToLine, ArrowUpFromLine, CircleDot, Plus, Trash2 } from 'lucide-react'
import { Button, Field, Panel, TextInput, Toggle } from '../ui/controls'
import { Chip, cx, EmptyState, IconButton, MethodTag } from '../ui/primitives'
import { PageShell } from './PageShell'

type Phase = 'request' | 'response'

interface BreakpointRule {
  id: string
  enabled: boolean
  url: string
  onRequest: boolean
  onResponse: boolean
}

interface PausedItem {
  id: string
  method: string
  url: string
  pausedAt: Phase
}

const INITIAL_RULES: BreakpointRule[] = [
  { id: 'r1', enabled: true, url: 'https://api.sniffy.dev/v1/*', onRequest: true, onResponse: false },
  { id: 'r2', enabled: true, url: 'https://*.example.com/checkout', onRequest: true, onResponse: true },
  { id: 'r3', enabled: false, url: 'https://cdn.assets.io/static/*.json', onRequest: false, onResponse: true },
]

const SAMPLE_PAUSED: PausedItem[] = [
  {
    id: 'p1',
    method: 'POST',
    url: 'https://api.sniffy.dev/v1/orders?source=web&token=eyJhbGciOiJIUzI1Ni',
    pausedAt: 'request',
  },
  {
    id: 'p2',
    method: 'GET',
    url: 'https://shop.example.com/checkout/summary?cartId=8821&currency=CNY',
    pausedAt: 'response',
  },
]

let ruleSeq = 100

export function BreakpointsView() {
  const [reqBreak, setReqBreak] = useState(true)
  const [respBreak, setRespBreak] = useState(false)
  const [rules, setRules] = useState<BreakpointRule[]>(INITIAL_RULES)
  const [paused, setPaused] = useState<PausedItem[]>(SAMPLE_PAUSED)

  const toggleRule = (id: string) =>
    setRules((rs) => rs.map((r) => (r.id === id ? { ...r, enabled: !r.enabled } : r)))

  const removeRule = (id: string) => setRules((rs) => rs.filter((r) => r.id !== id))

  const addRule = () => {
    ruleSeq += 1
    setRules((rs) => [
      ...rs,
      { id: `r${ruleSeq}`, enabled: true, url: 'https://', onRequest: true, onResponse: false },
    ])
  }

  const resolvePaused = (id: string) => setPaused((ps) => ps.filter((p) => p.id !== id))
  const restorePaused = () => setPaused(SAMPLE_PAUSED)

  return (
    <PageShell
      icon={CircleDot}
      title="断点"
      subtitle="拦截请求 / 响应，改包后手动放行"
      actions={
        <Button
          size="sm"
          variant="secondary"
          icon={<CircleDot className="h-3.5 w-3.5" />}
          onClick={restorePaused}
          disabled={paused.length > 0}
        >
          重放示例
        </Button>
      }
    >
      {/* 全局断点 */}
      <Panel title="全局断点" icon={<CircleDot className="h-4 w-4" />}>
        <Field label="请求断点" hint="命中后将在请求发出前暂停，等待你修改请求头 / 请求体并手动放行。">
          <Toggle checked={reqBreak} onChange={setReqBreak} />
        </Field>
        <Field label="响应断点" hint="命中后将在响应返回客户端前暂停，可在此修改状态码与响应内容。">
          <Toggle checked={respBreak} onChange={setRespBreak} />
        </Field>
      </Panel>

      {/* 断点规则 */}
      <Panel
        title="断点规则"
        icon={<CircleDot className="h-4 w-4" />}
        right={
          <Button size="sm" variant="secondary" icon={<Plus className="h-3.5 w-3.5" />} onClick={addRule}>
            添加规则
          </Button>
        }
      >
        {rules.length === 0 ? (
          <div className="px-3 py-6">
            <EmptyState
              icon={<CircleDot className="h-7 w-7" />}
              title="暂无断点规则"
              hint="添加 URL 匹配规则，命中的请求 / 响应将根据上方全局开关进行拦截。"
            />
          </div>
        ) : (
          rules.map((rule) => (
            <RuleRow
              key={rule.id}
              rule={rule}
              onToggle={() => toggleRule(rule.id)}
              onRemove={() => removeRule(rule.id)}
            />
          ))
        )}
      </Panel>

      {/* 暂停的请求 */}
      <Panel
        title="暂停的请求"
        icon={<CircleDot className="h-4 w-4" />}
        right={<Chip count={paused.length}>等待处理</Chip>}
      >
        {paused.length === 0 ? (
          <div className="px-3 py-8">
            <EmptyState
              icon={<CircleDot className="h-7 w-7" />}
              title="当前无暂停的请求"
              hint="开启上方断点后，命中的请求会在此等待处理"
            />
          </div>
        ) : (
          paused.map((item) => (
            <PausedRow key={item.id} item={item} onResolve={() => resolvePaused(item.id)} />
          ))
        )}
      </Panel>
    </PageShell>
  )
}

/* ───────────────────────── 规则行 ───────────────────────── */

function RuleRow({
  rule,
  onToggle,
  onRemove,
}: {
  rule: BreakpointRule
  onToggle: () => void
  onRemove: () => void
}) {
  return (
    <div className={cx('flex items-center gap-2.5 px-3 py-2', !rule.enabled && 'opacity-55')}>
      <Toggle checked={rule.enabled} onChange={onToggle} />
      <TextInput
        value={rule.url}
        readOnly
        width="100%"
        title={rule.url}
        className="flex-1 cursor-default font-mono text-[11.5px]"
      />
      <div className="flex shrink-0 items-center gap-1">
        {rule.onRequest && (
          <Chip active title="请求阶段拦截">
            请求
          </Chip>
        )}
        {rule.onResponse && (
          <Chip active title="响应阶段拦截">
            响应
          </Chip>
        )}
        {!rule.onRequest && !rule.onResponse && <Chip title="未选择拦截阶段">未启用</Chip>}
      </div>
      <IconButton size="sm" tone="danger" onClick={onRemove} title="删除规则">
        <Trash2 className="h-3.5 w-3.5" />
      </IconButton>
    </div>
  )
}

/* ───────────────────────── 暂停项行 ───────────────────────── */

function PausedRow({ item, onResolve }: { item: PausedItem; onResolve: () => void }) {
  const PhaseIcon = item.pausedAt === 'request' ? ArrowUpFromLine : ArrowDownToLine
  const phaseLabel = item.pausedAt === 'request' ? '请求' : '响应'

  return (
    <div className="flex items-center gap-2.5 px-3 py-2.5">
      <MethodTag method={item.method} className="w-12 shrink-0 text-center" />
      <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg-muted" title={item.url}>
        {item.url}
      </span>
      <span
        className={cx(
          'inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-px text-[10px] font-medium',
          item.pausedAt === 'request' ? 'bg-info/15 text-info' : 'bg-iris/15 text-iris',
        )}
        title={`暂停于：${phaseLabel}`}
      >
        <PhaseIcon className="h-3 w-3" />
        暂停于 {phaseLabel}
      </span>
      <div className="flex shrink-0 items-center gap-1.5">
        <Button variant="primary" size="sm" onClick={onResolve}>
          放行
        </Button>
        <Button size="sm">编辑</Button>
        <Button variant="danger" size="sm" onClick={onResolve}>
          阻断
        </Button>
      </div>
    </div>
  )
}
