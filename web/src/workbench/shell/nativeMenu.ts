import { useEffect, useRef } from 'react'
import { Events } from '@wailsio/runtime'
import { Bridge } from '@/lib/bridge'
import { detectPlatform } from '@/lib/platform'
import type { MenuItemNode, MenuNode, TopMenu } from '../ui/Menu'

/**
 * macOS 原生菜单栏适配器。
 *
 * macOS 的习惯是把菜单放在屏幕顶部的系统菜单栏，而不是窗口内自绘一行。本模块把 Workbench
 * 用 React 维护的 `TopMenu[]`（真相源：勾选态、会翻转的标签、onSelect 闭包）转成一棵可序列化的
 * 模型推给 Go（Bridge.SetMenu），由 Go 搭出原生 NSMenu；原生项被点中时 Go 发 "menu:clicked"
 * 事件带回该项的 id，这里查表执行对应 onSelect。
 *
 * 仅 macOS 启用：Windows/Linux 仍由 TitleBar 在窗口内自绘菜单。
 *
 * 快捷键不进原生菜单（不设 accelerator）：Workbench 的全局 keydown 已用 ctrlKey||metaKey 统一处理，
 * ⌘F/⌘J/⌘R/⌘E/⌘A 等在 mac 上本就生效。让原生菜单也持有加速键会与之重复触发（切换类会被点两次抵消），
 * 故这里只镜像结构与状态、不镜像加速键。
 */

/** 可映射到 Wails 原生菜单角色的字符串（与 internal/desktop/menu.go 的 menuRole 对应）。 */
type NativeRole =
  | 'about'
  | 'quit'
  | 'hide'
  | 'hideOthers'
  | 'showAll'
  | 'services'
  | 'undo'
  | 'redo'
  | 'cut'
  | 'copy'
  | 'paste'
  | 'selectAll'
  | 'minimise'
  | 'zoom'
  | 'close'

/** 推给 Go 的菜单模型节点（与 internal/desktop/menu.go 的 menuNode 形状对齐）。 */
export type NativeNode =
  | { kind: 'submenu'; label: string; items: NativeNode[] }
  | { kind: 'item'; id: string; label: string; checked?: boolean; disabled?: boolean }
  | { kind: 'separator' }
  | { kind: 'role'; role: NativeRole }

/** 应用菜单（Sniffy）里需要前端处理的动作——这些没有现成原生角色。 */
export interface AppMenuActions {
  openSettings: () => void
  openAbout: () => void
}

/**
 * 把 React 的 `TopMenu[]` 转成原生菜单模型 + id→onSelect 处理表。
 * 额外补齐 macOS 该有的部分：应用菜单（关于/设置/隐藏/退出）、「编辑」里注入剪贴板原生角色、「窗口」菜单。
 */
export function buildNativeMenu(menus: TopMenu[], actions: AppMenuActions): { spec: NativeNode[]; handlers: Map<string, () => void> } {
  const handlers = new Map<string, () => void>()
  let counter = 0
  // id 只需在一次快照内自洽即可：每次重建都重发整棵树并重建处理表。
  const reg = (fn: () => void): string => {
    const id = `h${counter++}`
    handlers.set(id, fn)
    return id
  }

  const mapNode = (node: MenuNode): NativeNode | null => {
    if ('type' in node && node.type === 'separator') return { kind: 'separator' }
    if ('type' in node && node.type === 'label') return null // 原生菜单无内联分组标题，略去
    const item = node as MenuItemNode
    if (item.submenu?.length) {
      return { kind: 'submenu', label: item.label, items: mapItems(item.submenu) }
    }
    return {
      kind: 'item',
      id: item.onSelect ? reg(item.onSelect) : '',
      label: item.label,
      ...(item.checked !== undefined ? { checked: item.checked } : {}),
      ...(item.disabled ? { disabled: true } : {}),
    }
  }
  const mapItems = (nodes: MenuNode[]): NativeNode[] => nodes.map(mapNode).filter((n): n is NativeNode => n !== null)

  // 应用菜单：macOS 把第一个子菜单当作以应用名加粗显示的「应用菜单」。
  const appMenu: NativeNode = {
    kind: 'submenu',
    label: 'Sniffy',
    items: [
      { kind: 'item', id: reg(actions.openAbout), label: '关于 Sniffy' },
      { kind: 'separator' },
      { kind: 'item', id: reg(actions.openSettings), label: '设置…' },
      { kind: 'separator' },
      { kind: 'role', role: 'services' },
      { kind: 'separator' },
      { kind: 'role', role: 'hide' },
      { kind: 'role', role: 'hideOthers' },
      { kind: 'role', role: 'showAll' },
      { kind: 'separator' },
      { kind: 'role', role: 'quit' },
    ],
  }

  // 剪贴板原生角色：注入「编辑」菜单开头，保证输入框里的 ⌘Z/⌘X/⌘C/⌘V 走系统实现。
  // （原先 mac 用的是 Wails 默认菜单，含这套 Edit；换成自定义菜单后必须自己带上，否则复制粘贴会失灵。）
  const clipboard: NativeNode[] = [
    { kind: 'role', role: 'undo' },
    { kind: 'role', role: 'redo' },
    { kind: 'separator' },
    { kind: 'role', role: 'cut' },
    { kind: 'role', role: 'copy' },
    { kind: 'role', role: 'paste' },
    { kind: 'separator' },
  ]

  // 窗口菜单：给 ⌘M 最小化 / ⌘W 关闭（子窗口在 mac 上靠它和红色按钮关闭）。
  const windowMenu: NativeNode = {
    kind: 'submenu',
    label: '窗口',
    items: [
      { kind: 'role', role: 'minimise' },
      { kind: 'role', role: 'zoom' },
      { kind: 'separator' },
      { kind: 'role', role: 'close' },
    ],
  }

  const businessMenus: NativeNode[] = menus.map((m) => {
    const items = mapItems(m.items)
    return { kind: 'submenu', label: m.label, items: m.label === '编辑' ? [...clipboard, ...items] : items }
  })

  // 顺序：应用菜单 → 业务菜单（按 mac 习惯在「帮助」前插入「窗口」）。
  const helpIdx = businessMenus.findIndex((x) => x.kind === 'submenu' && x.label === '帮助')
  const ordered =
    helpIdx >= 0
      ? [...businessMenus.slice(0, helpIdx), windowMenu, ...businessMenus.slice(helpIdx)]
      : [...businessMenus, windowMenu]

  return { spec: [appMenu, ...ordered], handlers }
}

/**
 * 把 Workbench 的菜单镜像到 macOS 顶部系统菜单栏。非 mac 平台为空操作。
 * menus 变化（含勾选/标签等状态变化）时重发整棵树；原生点击经 "menu:clicked" 回桥执行。
 */
export function useNativeMenu(menus: TopMenu[], actions: AppMenuActions): void {
  const isMac = detectPlatform() === 'mac'
  // 处理表每次重建；监听器只注册一次，故从 ref 读最新表。
  const handlersRef = useRef<Map<string, () => void>>(new Map())
  // actions 每次渲染可能换引用；放 ref 里避免推送 effect 被它的身份变化反复触发。
  const actionsRef = useRef(actions)
  actionsRef.current = actions

  useEffect(() => {
    if (!isMac) return
    let off: (() => void) | undefined
    try {
      off = Events.On('menu:clicked', (e: { data?: unknown }) => {
        const id = Array.isArray(e?.data) ? e.data[0] : e?.data
        if (typeof id === 'string') handlersRef.current.get(id)?.()
      })
    } catch {
      // 非 Wails 环境：Events 不可用，忽略。
    }
    return () => {
      try {
        off?.()
      } catch {
        /* ignore */
      }
    }
  }, [isMac])

  useEffect(() => {
    if (!isMac) return
    const { spec, handlers } = buildNativeMenu(menus, actionsRef.current)
    handlersRef.current = handlers
    void Bridge.setMenu(spec).catch(() => {})
  }, [isMac, menus])
}
