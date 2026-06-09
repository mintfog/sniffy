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
//                  Checked 非 nil 时渲染为勾选项；Disabled 置灰。
//   - "separator"：分隔线。
//   - "role"     ：映射到 Wails 原生角色（复制/粘贴/退出/隐藏/最小化等），由系统实现，无需回桥。
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
}

// addMenuNode 把一个前端节点接到原生菜单 parent 下。
func addMenuNode(parent *application.Menu, n menuNode) {
	switch n.Kind {
	case "separator":
		parent.AddSeparator()
	case "role":
		if r, ok := menuRole(n.Role); ok {
			parent.AddRole(r)
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
