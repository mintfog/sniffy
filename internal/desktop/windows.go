// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// mainWindowName 是主窗口的名称（run.go 创建时设置），供 FocusMain / 子窗口请求导航时定位。
const mainWindowName = "main"

// winSpec 描述一个可弹出的独立子窗口的外观。
type winSpec struct {
	title               string
	width, height       int
	minWidth, minHeight int
}

// 仅这几类「工具型」页面允许弹独立系统窗口（与前端 StandaloneWindow 对应）。
var winSpecs = map[string]winSpec{
	"settings": {title: "Sniffy 设置", width: 760, height: 780, minWidth: 560, minHeight: 480},
	"tools":    {title: "Sniffy 工具箱", width: 600, height: 720, minWidth: 460, minHeight: 420},
	"about":    {title: "关于 Sniffy", width: 440, height: 560, minWidth: 380, minHeight: 420},
	// 插件工作室：列表 + 代码编辑 + 日志同屏，需要更大的默认尺寸。
	"plugins": {title: "Sniffy 插件", width: 1180, height: 800, minWidth: 900, minHeight: 560},
}

// OpenWindow 打开（或聚焦已存在的）承载某个页面的独立系统窗口。
// view ∈ {settings, tools, about}；query 为可选的附加查询串（如 "tool=base64dec"）。
func (b *Bridge) OpenWindow(view, query string) {
	spec, ok := winSpecs[view]
	if !ok {
		return
	}
	app := application.Get()
	if app == nil {
		return
	}

	name := "sniffy-" + view
	if win, exists := app.Window.GetByName(name); exists {
		// 仅在最小化时还原；否则 Restore() 会把已最大化的窗口缩回默认尺寸。
		if win.IsMinimised() {
			win.Restore()
		}
		win.Focus()
		return
	}

	url := "/?w=" + view
	if query != "" {
		url += "&" + query
	}

	opts := application.WebviewWindowOptions{
		Name:             name,
		Title:            spec.title,
		Width:            spec.width,
		Height:           spec.height,
		MinWidth:         spec.minWidth,
		MinHeight:        spec.minHeight,
		URL:              url,
		BackgroundColour: application.NewRGB(17, 17, 23),
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
	}
	ApplyPlatformChrome(&opts)
	app.Window.NewWithOptions(opts)
}

// SaveTextFile 弹出系统「保存文件」对话框，并由 Go 直接把内容写到用户选定的路径。
// 用于导出证书 / HAR / JSON / 二维码等——避免走 WebView 的浏览器下载栏，观感原生。
// 返回是否已保存（用户取消或出错返回 false）。
func (b *Bridge) SaveTextFile(defaultName, content string) bool {
	app := application.Get()
	if app == nil {
		return false
	}
	dlg := app.Dialog.SaveFile()
	dlg.SetFilename(defaultName)
	if ext := filepath.Ext(defaultName); ext != "" {
		dlg.AddFilter(strings.ToUpper(strings.TrimPrefix(ext, "."))+" 文件", "*"+ext)
	}
	if w := app.Window.Current(); w != nil {
		dlg.AttachToWindow(w)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return false
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false
	}
	return true
}

// FocusMain 把主窗口带到前台（供子窗口请求主窗口导航时配合使用）。
func (b *Bridge) FocusMain() {
	app := application.Get()
	if app == nil {
		return
	}
	if win, ok := app.Window.GetByName(mainWindowName); ok {
		if win.IsMinimised() {
			win.Restore()
		}
		win.Focus()
	}
}
