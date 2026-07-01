// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"errors"
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
	// 插件工作室：源列表 + 代码/配置/日志 主从同屏，需要更大的默认尺寸。
	"plugins": {title: "Sniffy 插件", width: 1040, height: 720, minWidth: 820, minHeight: 520},
	// 重写规则：规则列表 + 匹配/动作编辑主从。
	"rules": {title: "Sniffy 重写规则", width: 960, height: 700, minWidth: 720, minHeight: 480},
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

// ExportCACertAs 以选定格式导出根证书:弹系统"保存文件"对话框并直接写盘。
// format ∈ {pem, crt, der, p12, bundle};password 仅对 p12 生效。
// 返回值:(true,nil)=已保存 / (false,nil)=用户取消 / (false,err)=真实错误。
// 先对话框再 encode:PKCS12 走 PBKDF2 不便宜,取消时不白算。
func (b *Bridge) ExportCACertAs(format, password string) (bool, error) {
	app := application.Get()
	if app == nil {
		return false, nil
	}
	dlg := app.Dialog.SaveFile()
	defaultName, filterLabel, ext := caExportDialogHints(format)
	dlg.SetFilename(defaultName)
	if ext != "" {
		dlg.AddFilter(filterLabel, "*"+ext)
	}
	if w := app.Window.Current(); w != nil {
		dlg.AttachToWindow(w)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return false, nil
	}

	data, _, err := b.app.ExportCAAs(format, password)
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, errors.New("导出内容为空")
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// PickImportCAFile 弹系统"打开文件"对话框选择要导入的根证书文件,返回绝对路径;
// 用户取消或出错返回空串。同时接受 .p12/.pfx 与 .pem/.crt(联合格式)。
func (b *Bridge) PickImportCAFile() string {
	app := application.Get()
	if app == nil {
		return ""
	}
	dlg := app.Dialog.OpenFile()
	dlg.SetTitle("选择根证书文件")
	dlg.AddFilter("PKCS#12 (.p12 / .pfx)", "*.p12;*.pfx")
	dlg.AddFilter("PEM Bundle (.pem / .crt)", "*.pem;*.crt")
	dlg.AddFilter("所有文件", "*.*")
	if w := app.Window.Current(); w != nil {
		dlg.AttachToWindow(w)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return ""
	}
	return path
}

// ImportCAFromFile 用给定文件路径 + 口令导入根 CA,返回新根的 PEM;
// 失败时返回错误(前端 Call.ByName 会得到 rejected Promise,可直接 .catch)。
func (b *Bridge) ImportCAFromFile(path, password string) (string, error) {
	return b.app.ImportCAFromFile(path, password)
}

// caExportDialogHints 按格式返回文件对话框的默认名/过滤器名/扩展名。
func caExportDialogHints(format string) (defaultName, filterLabel, ext string) {
	switch strings.ToLower(format) {
	case "der":
		return "sniffy-ca.der", "DER 证书", ".der"
	case "p12", "pfx":
		return "sniffy-ca.p12", "PKCS#12 (.p12)", ".p12"
	case "bundle":
		return "sniffy-ca-bundle.pem", "PEM Bundle", ".pem"
	case "pem":
		return "sniffy-ca.pem", "PEM 证书", ".pem"
	default:
		return "sniffy-ca.crt", "CRT 证书", ".crt"
	}
}
