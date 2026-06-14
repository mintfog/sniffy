// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// menuNode 是前端（web/src/workbench/shell/nativeMenu.ts）发来的可序列化菜单模型节点。
//
// kind 取值：
//   - "submenu"  ：子菜单，含 Label + Items（顶层每个菜单也是 submenu）。
//   - "item"     ：可点击项。ID 非空时，点击经 "menu:clicked" 事件回桥前端执行其 onSelect；
//     Checked 非 nil 时渲染为勾选项；Disabled 置灰。
//   - "separator"：分隔线。
//   - "role"     ：映射到 Wails 原生角色（复制/粘贴/退出/隐藏/最小化等），由系统实现，无需回桥；
//     Label 非空时覆盖 Wails 内置的英文标签（"Undo"/"Quit Sniffy"…）。
//
// 之所以让前端发模型、Go 这边照搭：菜单的真相源是前端 React（勾选态/会翻转的标签/onSelect 闭包），
// 原生菜单只是它在 macOS 顶栏的一面镜子。
type menuNode struct {
	Kind     string     `json:"kind"`
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Role     string     `json:"role"`
	Checked  *bool      `json:"checked"`
	Disabled bool       `json:"disabled"`
	Items    []menuNode `json:"items"`
}

// SetMenu 用前端发来的模型重建 macOS 顶部系统菜单栏（应用菜单）。
//
// 仅 macOS 生效：Windows/Linux 的菜单仍由前端 TitleBar 在窗口内自绘，不会调用此方法。
// 前端每次菜单状态变化（勾选/标签/视图切换）都会重发整棵树，这里整体重建以保持同步——
// 这些变化都是低频的（点菜单、切视图），整树重建足够廉价且最不易错。
func (b *Bridge) SetMenu(items []menuNode) {
	if runtime.GOOS != "darwin" {
		return
	}
	appl := application.Get()
	if appl == nil {
		return
	}
	// 在主线程上换主菜单：SetApplicationMenu 内部会 InvokeSync 构建 NSMenu，而
	// dispatchOnMainThread 在主线程内联执行，故这里嵌套 InvokeSync 不会死锁；
	// 同时保证 [NSApp setMainMenu:] 也落在主线程。
	application.InvokeSync(func() {
		root := application.NewMenu()
		for _, top := range items {
			addMenuNode(root, top)
		}
		appl.Menu.SetApplicationMenu(root)
	})
	// AppKit 装主菜单时可能向“编辑”菜单尾部自动追加英文系统项（听写/表情/自动填充/写作工具），
	// NSDisabled* 默认值（appkit_darwin.go）拦不住的在这里按自建项数兜底修剪。
	for _, top := range items {
		if top.Kind == "submenu" && isEditMenu(top) {
			pruneMenuTail(top.Label, renderedCount(top.Items))
			break
		}
	}
}

// isEditMenu 判断一个子菜单是否为「编辑」菜单：含任一剪贴板原生角色即是。
// 前端（nativeMenu.ts）会把 undo/redo/cut/copy/paste 角色注入「编辑」菜单开头，
// 故以此识别比匹配会随语言变化的标签更稳。
func isEditMenu(n menuNode) bool {
	for _, c := range n.Items {
		if c.Kind == "role" {
			switch c.Role {
			case "paste", "copy", "cut", "undo", "redo":
				return true
			}
		}
	}
	return false
}

// renderedCount 统计 items 实际生成的 NSMenuItem 数。须与 addMenuNode 的生成逻辑一致：
// 唯一不生成菜单项的情形是未知角色（menuRole 返回 false）。
func renderedCount(items []menuNode) int {
	n := 0
	for _, it := range items {
		if it.Kind == "role" {
			if _, ok := menuRole(it.Role); !ok {
				continue
			}
		}
		n++
	}
	return n
}

// addMenuNode 把一个前端节点接到原生菜单 parent 下。
func addMenuNode(parent *application.Menu, n menuNode) {
	switch n.Kind {
	case "separator":
		parent.AddSeparator()
	case "role":
		if r, ok := menuRole(n.Role); ok {
			addRoleItem(parent, r, n.Label)
		}
	case "submenu":
		sub := parent.AddSubmenu(n.Label)
		for _, c := range n.Items {
			addMenuNode(sub, c)
		}
	default: // "item"
		var mi *application.MenuItem
		if n.Checked != nil {
			mi = parent.AddCheckbox(n.Label, *n.Checked)
		} else {
			mi = parent.Add(n.Label)
		}
		if n.Disabled {
			mi.SetEnabled(false)
		}
		if id := n.ID; id != "" {
			mi.OnClick(func(*application.Context) {
				if a := application.Get(); a != nil {
					a.Event.Emit("menu:clicked", id)
				}
			})
		}
	}
}

// addRoleItem 把一个原生角色项接到 parent 下。label 非空时覆盖 Wails 为角色硬编码的英文标签——
// 角色行为在 macOS 上由原生 selector 实现，与标签无关，改标签不影响功能。
func addRoleItem(parent *application.Menu, role application.Role, label string) {
	mi := application.NewRole(role)
	if mi == nil {
		return
	}
	if label != "" {
		mi.SetLabel(label)
	}
	// Menu 没有挂接现成 MenuItem 的方法，借 NewMenuFromItems+Append 实现。
	parent.Append(application.NewMenuFromItems(mi))
}

// startupMacMenu 是启动期占位菜单：Wails 在应用菜单缺省时会装一套默认英文菜单
// （App/File/Edit/View/Window/Help），而前端要等 React 挂载后才经 SetMenu 推送完整菜单。
// 启动时先装这套最小菜单（按机器语言渲染，见 locale.go），避免英文菜单闪现（或前端加载失败时残留）。
// 结构与 nativeMenu.ts 推送的系统部分保持一致，前端就绪后会整树替换。
// 返回值另含“编辑”菜单标签（供 pruneMenuTail 定位）与其子项数（供兜底修剪）。
func startupMacMenu(lb menuLabels) (menu *application.Menu, editLabel string, editItemCount int) {
	root := application.NewMenu()

	appMenu := root.AddSubmenu("Sniffy")
	addRoleItem(appMenu, application.About, lb.about)
	appMenu.AddSeparator()
	addRoleItem(appMenu, application.ServicesMenu, lb.services)
	appMenu.AddSeparator()
	addRoleItem(appMenu, application.Hide, lb.hide)
	addRoleItem(appMenu, application.HideOthers, lb.hideOthers)
	addRoleItem(appMenu, application.ShowAll, lb.showAll)
	appMenu.AddSeparator()
	addRoleItem(appMenu, application.Quit, lb.quit)

	edit := root.AddSubmenu(lb.edit)
	addRoleItem(edit, application.Undo, lb.undo)
	addRoleItem(edit, application.Redo, lb.redo)
	edit.AddSeparator()
	addRoleItem(edit, application.Cut, lb.cut)
	addRoleItem(edit, application.Copy, lb.copy)
	addRoleItem(edit, application.Paste, lb.paste)
	const editCount = 6 // 上面 6 行，与构建保持同步

	win := root.AddSubmenu(lb.window)
	addRoleItem(win, application.Minimise, lb.minimise)
	addRoleItem(win, application.Zoom, lb.zoom)
	win.AddSeparator()
	addRoleItem(win, application.CloseWindow, lb.closeWindow)

	return root, lb.edit, editCount
}

// menuRole 把前端的角色字符串映射到 Wails 原生菜单角色。第二个返回值为 false 表示未知角色（忽略）。
func menuRole(role string) (application.Role, bool) {
	switch role {
	case "about":
		return application.About, true
	case "quit":
		return application.Quit, true
	case "hide":
		return application.Hide, true
	case "hideOthers":
		return application.HideOthers, true
	case "showAll":
		return application.ShowAll, true
	case "services":
		return application.ServicesMenu, true
	case "undo":
		return application.Undo, true
	case "redo":
		return application.Redo, true
	case "cut":
		return application.Cut, true
	case "copy":
		return application.Copy, true
	case "paste":
		return application.Paste, true
	case "selectAll":
		return application.SelectAll, true
	case "minimise":
		return application.Minimise, true
	case "zoom":
		return application.Zoom, true
	case "close":
		return application.CloseWindow, true
	}
	return application.NoRole, false
}
