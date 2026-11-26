// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/mintfog/sniffy/internal/procinfo"
	"github.com/mintfog/sniffy/internal/service"
)

// ---- 虚拟网卡按应用抓取的名单 ----

// TunAppChange 是会话右键「只抓 / 不抓这个应用」之后的结果：加入或移出名单的条目，与改后的配置。
type TunAppChange struct {
	App    string             `json:"app"`
	Config service.ConfigView `json:"config"`
}

// SetTunAppCaptured 见 service.Service.SetTunAppCaptured。
func (b *Bridge) SetTunAppCaptured(path, name string, capture bool) (TunAppChange, error) {
	app, c, err := b.app.Service.SetTunAppCaptured(path, name, capture)
	return TunAppChange{App: app, Config: service.PublicConfig(c)}, err
}

func (b *Bridge) GetNetworkApps() ([]procinfo.AppChoice, error) { return b.app.Service.NetworkApps() }
func (b *Bridge) GetInstalledApps() ([]procinfo.AppChoice, error) {
	return b.app.Service.InstalledApps()
}
func (b *Bridge) GetAppIcons(paths []string) []procinfo.AppIcon { return b.app.Service.AppIcons(paths) }
func (b *Bridge) GetTunAppsRunning(apps []string) ([]bool, error) {
	return b.app.Service.TunAppsRunning(apps)
}

// PickProgram 弹出系统的选择文件对话框，返回选中的程序路径，取消时为空。title 是对话框标题（已按界面语言）。
func (b *Bridge) PickProgram(title string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", nil
	}
	dlg := app.Dialog.OpenFile()
	dlg.SetTitle(title)
	if w := app.Window.Current(); w != nil {
		dlg.AttachToWindow(w)
	}
	return dlg.PromptForSingleSelection()
}
