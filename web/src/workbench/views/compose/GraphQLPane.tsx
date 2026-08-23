/**
 * GraphQL 的 Query / Variables 编辑区。
 *
 * GraphQL 是纯前端外壳：出线的仍是一条 POST + JSON body，合成在 wire.ts 里做。
 * 编辑器没有 GraphQL 语法模式（只装了 @codemirror/lang-javascript），不为此新增依赖，
 * 两个编辑器都退回 json 档——它只提供行号 / 括号匹配，不做语言相关的补全。
 */
import { useTranslation } from 'react-i18next'
import { PluginEditor } from '../plugins/editor'
import { cx } from '../../ui/primitives'
import { gqlOf, type Draft, type GraphQLPart } from './model'
import { resolveWire } from './wire'

export function GraphQLPane({
  draft,
  mode,
  onPatch,
}: {
  draft: Draft
  mode: 'query' | 'variables'
  onPatch: (patch: Partial<Draft>) => void
}) {
  const { t } = useTranslation()
  const part = gqlOf(draft)
  const edit = (p: Partial<GraphQLPart>) => onPatch({ graphql: { ...part, ...p } })

  if (mode === 'variables') {
    const { varsError } = resolveWire(draft)
    return (
      <div className="flex h-full min-h-0 flex-col">
        {varsError && (
          <div className="border-b border-line bg-danger/10 px-3 py-1.5 text-2xs leading-relaxed text-danger">
            {t('compose.graphql.variablesInvalid', { reason: varsError })}
          </div>
        )}
        <div className="min-h-0 flex-1">
          <PluginEditor
            value={part.variables}
            onChange={(v) => edit({ variables: v })}
            language="json"
            placeholder={t('compose.graphql.variablesPlaceholder')}
            ariaLabel={t('compose.graphql.variables')}
            className="h-full"
          />
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b border-line bg-inset/60 px-3 py-1.5">
        <span className={cx('shrink-0 text-2xs', part.query.trim() ? 'text-fg-faint' : 'text-warn')}>
          {part.query.trim() ? t('compose.graphql.endpointHint') : t('compose.graphql.queryRequired')}
        </span>
        <input
          value={part.operationName}
          spellCheck={false}
          onChange={(e) => edit({ operationName: e.target.value })}
          placeholder={t('compose.graphql.operationNamePlaceholder')}
          aria-label={t('compose.graphql.operationName')}
          title={t('compose.graphql.operationName')}
          className="ml-auto h-6 min-w-0 flex-1 rounded-wb border border-line bg-inset px-2 font-mono text-[11.5px] text-fg outline-none transition-colors placeholder:font-sans placeholder:text-fg-faint focus:border-accent focus:bg-surface"
        />
      </div>
      <div className="min-h-0 flex-1">
        <PluginEditor
          value={part.query}
          onChange={(v) => edit({ query: v })}
          language="json"
          placeholder={t('compose.graphql.queryPlaceholder')}
          ariaLabel={t('compose.graphql.query')}
          className="h-full"
        />
      </div>
    </div>
  )
}
