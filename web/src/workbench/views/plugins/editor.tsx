import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { Compartment, EditorState, type Extension } from '@codemirror/state'
import {
  EditorView,
  keymap,
  lineNumbers,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  drawSelection,
  rectangularSelection,
  placeholder as cmPlaceholder,
} from '@codemirror/view'
import {
  defaultKeymap,
  history,
  historyKeymap,
  indentWithTab,
} from '@codemirror/commands'
import {
  HighlightStyle,
  syntaxHighlighting,
  indentOnInput,
  bracketMatching,
  indentUnit,
} from '@codemirror/language'
import {
  autocompletion,
  completionKeymap,
  closeBrackets,
  closeBracketsKeymap,
} from '@codemirror/autocomplete'
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search'
import { javascript, javascriptLanguage } from '@codemirror/lang-javascript'
import { tags as t } from '@lezer/highlight'
import { sniffyCompletionSource } from './completions'

/** 语法配色：把 lezer 标签映射到工作台语义色，跟随明暗主题（用 CSS 变量）。 */
const sniffyHighlight = HighlightStyle.define([
  { tag: [t.keyword, t.moduleKeyword, t.controlKeyword, t.operatorKeyword], color: 'rgb(var(--c-accent))' },
  { tag: [t.string, t.special(t.string)], color: 'rgb(var(--c-ok))' },
  { tag: [t.number, t.bool, t.null, t.atom], color: 'rgb(var(--c-warn))' },
  { tag: [t.comment, t.lineComment, t.blockComment], color: 'rgb(var(--c-fg-muted))', fontStyle: 'italic' },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: 'rgb(var(--c-info))' },
  { tag: [t.variableName, t.definition(t.variableName), t.propertyName], color: 'rgb(var(--c-fg))' },
  { tag: [t.typeName, t.className, t.namespace], color: 'rgb(var(--c-violet))' },
  { tag: [t.operator, t.punctuation, t.separator, t.bracket, t.brace, t.paren], color: 'rgb(var(--c-fg-muted))' },
  { tag: [t.regexp, t.escape], color: 'rgb(var(--c-warn))' },
  { tag: [t.invalid], color: 'rgb(var(--c-danger))' },
])

/** 编辑器主题：全部用工作台 CSS 变量上色，主题切换时自动重绘，无需重建。 */
const sniffyTheme = EditorView.theme({
  '&': { height: '100%', color: 'rgb(var(--c-fg))', backgroundColor: 'rgb(var(--c-inset))', fontSize: '12.5px' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { fontFamily: "'JetBrains Mono Variable', 'JetBrains Mono', ui-monospace, monospace", lineHeight: '1.6' },
  '.cm-content': { caretColor: 'rgb(var(--c-accent))', padding: '8px 0' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'rgb(var(--c-accent))' },
  '.cm-gutters': { backgroundColor: 'transparent', color: 'rgb(var(--c-fg-faint))', border: 'none' },
  '.cm-activeLineGutter': { backgroundColor: 'rgb(var(--c-elevated) / 0.45)', color: 'rgb(var(--c-fg-muted))' },
  '.cm-activeLine': { backgroundColor: 'rgb(var(--c-elevated) / 0.30)' },
  // 选区用半透明选中蓝:比中性灰多一层色相差;不用实底,语法字色须保持可读
  '.cm-selectionBackground': { backgroundColor: 'rgb(var(--wb-selection) / 0.35)' },
  '&.cm-focused .cm-selectionBackground': { backgroundColor: 'rgb(var(--wb-selection) / 0.45)' },
  // 原生 ::selection 置透明并还原文字色(全局 ::selection 是实底+白字),只留 drawSelection 画的层。
  '.cm-content ::selection': { backgroundColor: 'transparent', color: 'currentColor' },
  '.cm-matchingBracket, &.cm-focused .cm-matchingBracket': {
    backgroundColor: 'rgb(var(--c-accent) / 0.22)',
    outline: '1px solid rgb(var(--c-accent) / 0.45)',
  },
  '.cm-selectionMatch': { backgroundColor: 'rgb(var(--c-accent) / 0.20)' },
  // 搜索命中沿用工作台查找高亮语言(不写则回退 CM 硬编码黄,和主题脱节)
  '.cm-searchMatch': { backgroundColor: 'rgb(var(--c-warn) / var(--wb-find-alpha, 0.4))' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'rgb(var(--wb-selection) / 0.6)' },
  '.cm-tooltip': {
    backgroundColor: 'rgb(var(--c-surface))',
    border: '1px solid rgb(var(--c-line-strong))',
    borderRadius: '6px',
    boxShadow: 'var(--wb-shadow)',
    overflow: 'hidden',
  },
  '.cm-tooltip.cm-tooltip-autocomplete > ul': {
    fontFamily: "'JetBrains Mono Variable', ui-monospace, monospace",
    fontSize: '12px',
    maxHeight: '16em',
  },
  '.cm-tooltip-autocomplete > ul > li': { padding: '2px 8px', color: 'rgb(var(--c-fg-muted))' },
  // 选中项实底 sel(补全条目是纯前景色文字,无语法高亮,不存在选区那种同色相糊的问题)
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': {
    backgroundColor: 'rgb(var(--c-sel))',
    color: 'rgb(var(--c-sel-fg))',
  },
  '.cm-tooltip-autocomplete > ul > li[aria-selected] .cm-completionLabel': { color: 'rgb(var(--c-sel-fg))' },
  '.cm-tooltip-autocomplete > ul > li[aria-selected] .cm-completionDetail': { color: 'rgb(var(--c-sel-fg) / 0.75)' },
  '.cm-completionLabel': { color: 'rgb(var(--c-fg))' },
  '.cm-completionDetail': { color: 'rgb(var(--c-fg-faint))', fontStyle: 'normal', marginLeft: '0.75em' },
  '.cm-completionInfo': {
    backgroundColor: 'rgb(var(--c-surface))',
    border: '1px solid rgb(var(--c-line-strong))',
    borderRadius: '6px',
    color: 'rgb(var(--c-fg-muted))',
    padding: '6px 8px',
    maxWidth: '320px',
  },
  '.cm-panels': { backgroundColor: 'rgb(var(--c-surface))', color: 'rgb(var(--c-fg))' },
  '.cm-panels.cm-panels-bottom': { borderTop: '1px solid rgb(var(--c-line))' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid rgb(var(--c-line))' },
  '.cm-panel.cm-search': { padding: '6px 24px 6px 8px' },
  // CM 基础主题给 .cm-button 铺的是亮色渐变(编辑器未声明 dark,恒中 &light 分支),
  // 只覆盖 backgroundColor 会被渐变整片盖住 —— 深色下就成了白底白字的空按钮。
  '.cm-button': {
    backgroundImage: 'none',
    backgroundColor: 'rgb(var(--c-elevated))',
    color: 'rgb(var(--c-fg))',
    border: '1px solid rgb(var(--c-line-strong))',
    borderRadius: 'var(--wb-radius-control)',
    fontSize: '11.5px',
    padding: '3px 10px',
    cursor: 'pointer',
  },
  '.cm-button:hover': { backgroundColor: 'rgb(var(--c-surface))', borderColor: 'rgb(var(--c-accent) / 0.6)' },
  '.cm-button:active': { backgroundImage: 'none', backgroundColor: 'rgb(var(--c-inset))' },
  '.cm-textfield': {
    backgroundColor: 'rgb(var(--c-inset))',
    color: 'rgb(var(--c-fg))',
    border: '1px solid rgb(var(--c-line))',
    borderRadius: 'var(--wb-radius-control)',
    fontSize: '12px',
    padding: '3px 8px',
  },
  '.cm-textfield:focus': { outline: 'none', borderColor: 'rgb(var(--c-accent))' },
  '.cm-panel label': { color: 'rgb(var(--c-fg-muted))', fontSize: '11.5px' },
  // 原生勾选框在亮色 color-scheme 下是刺眼的白方块,跟着面板走深/亮
  '.cm-panel input[type=checkbox]': { accentColor: 'rgb(var(--c-accent))' },
  ":root[data-theme='dark'] & .cm-panel input[type=checkbox]": { colorScheme: 'dark' },
  '.cm-panel.cm-search [name=close], .cm-dialog-close': {
    color: 'rgb(var(--c-fg-faint))',
    fontSize: '15px',
    lineHeight: '1',
    cursor: 'pointer',
  },
  '.cm-panel.cm-search [name=close]:hover, .cm-dialog-close:hover': { color: 'rgb(var(--c-fg))' },
})

/**
 * CM 内置面板(查找/替换、跳转到行)的英文文案：其按钮与占位符都取自 EditorState.phrases，
 * 键即英文原文，按当前界面语言译过去。
 */
function searchPhrases(t: TFunction): Record<string, string> {
  return {
    Find: t('editor.search.find'),
    Replace: t('editor.search.replace'),
    next: t('editor.search.next'),
    previous: t('editor.search.previous'),
    all: t('editor.search.all'),
    'match case': t('editor.search.caseSensitive'),
    regexp: t('editor.search.regexp'),
    'by word': t('editor.search.byWord'),
    replace: t('editor.search.replaceOne'),
    'replace all': t('editor.search.replaceAll'),
    close: t('editor.search.close'),
    'Go to line': t('editor.search.goto'),
    go: t('editor.search.gotoSubmit'),
  }
}

/** 语言可运行时切换，phrases 隔一个 compartment 重配置，不重建编辑器（已打开的面板下次打开才换文案）。 */
const phrasesCompartment = new Compartment()

interface PluginEditorProps {
  value: string
  onChange: (v: string) => void
  /** Cmd/Ctrl+S 触发保存。 */
  onSave?: () => void
  language?: 'js' | 'json'
  readOnly?: boolean
  placeholder?: string
  className?: string
  ariaLabel?: string
}

/** 基于 CodeMirror 6 的轻量代码编辑器：JS 语法高亮 + 宿主 API 补全 + Cmd/Ctrl+S 保存。 */
export function PluginEditor({ value, onChange, onSave, language = 'js', readOnly = false, placeholder, className, ariaLabel }: PluginEditorProps) {
  const elRef = useRef<HTMLDivElement | null>(null)
  const viewRef = useRef<EditorView | null>(null)

  // 回调与最新值用 ref 持有，避免重建编辑器（重建会丢失光标/撤销栈/滚动位置）。
  const valueRef = useRef(value)
  valueRef.current = value
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange
  const onSaveRef = useRef(onSave)
  onSaveRef.current = onSave

  const { t } = useTranslation()
  const phrases = useMemo(() => searchPhrases(t), [t])
  const phrasesRef = useRef(phrases)
  phrasesRef.current = phrases

  useEffect(() => {
    if (!elRef.current) return

    const saveKey = keymap.of([
      { key: 'Mod-s', preventDefault: true, run: () => { onSaveRef.current?.(); return true } },
    ])

    const langExt: Extension =
      language === 'js'
        ? [
            javascript(),
            javascriptLanguage.data.of({ autocomplete: sniffyCompletionSource }),
            autocompletion({ activateOnTyping: true, defaultKeymap: true, icons: true }),
          ]
        : javascript() // JSON 走 JS 高亮即可，不挂宿主 API 补全

    const extensions: Extension[] = [
      lineNumbers(),
      highlightActiveLineGutter(),
      highlightSpecialChars(),
      history(),
      drawSelection(),
      rectangularSelection(),
      EditorState.allowMultipleSelections.of(true),
      indentOnInput(),
      indentUnit.of('  '),
      bracketMatching(),
      closeBrackets(),
      highlightActiveLine(),
      highlightSelectionMatches(),
      syntaxHighlighting(sniffyHighlight),
      sniffyTheme,
      phrasesCompartment.of(EditorState.phrases.of(phrasesRef.current)),
      langExt,
      keymap.of([
        ...closeBracketsKeymap,
        ...defaultKeymap,
        ...historyKeymap,
        ...completionKeymap,
        ...searchKeymap,
        indentWithTab,
      ]),
      saveKey,
      EditorState.readOnly.of(readOnly),
      EditorView.editable.of(!readOnly),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) onChangeRef.current(u.state.doc.toString())
      }),
    ]
    if (placeholder) extensions.push(cmPlaceholder(placeholder))

    const view = new EditorView({
      state: EditorState.create({ doc: valueRef.current, extensions }),
      parent: elRef.current,
    })
    if (ariaLabel) view.contentDOM.setAttribute('aria-label', ariaLabel)
    viewRef.current = view
    return () => {
      view.destroy()
      viewRef.current = null
    }
    // 仅在语言/只读切换时重建（这两者在单个实例的生命周期里基本不变）。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [language, readOnly])

  useEffect(() => {
    viewRef.current?.dispatch({
      effects: phrasesCompartment.reconfigure(EditorState.phrases.of(phrases)),
    })
  }, [phrases])

  // 外部 value 变化（如切换插件、插入模板）时同步到文档；与文档一致则不动，避免打断输入。
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const cur = view.state.doc.toString()
    if (value !== cur) {
      view.dispatch({ changes: { from: 0, to: cur.length, insert: value } })
    }
  }, [value])

  return <div ref={elRef} className={className} />
}
