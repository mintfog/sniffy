// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/update"
)

// GetUpdateState 供窗口打开时回填,后续变化经 update_state 事件推送。
func (b *Bridge) GetUpdateState() service.UpdateStateDTO { return b.app.Service.UpdateState() }

// CheckUpdate 立即查询一次发布清单并返回新状态。清单源连不上时状态里带失败原因,
// 不作为调用错误抛给前端。
func (b *Bridge) CheckUpdate() service.UpdateStateDTO {
	state, _ := b.app.Service.CheckForUpdate(context.Background())
	return state
}

// DownloadUpdate 启动后台下载并返回当前快照,后续进度经 update_state 事件推送。
// 当前平台没有对应产物时返回错误。
func (b *Bridge) DownloadUpdate() (service.UpdateStateDTO, error) {
	return b.app.Service.StartUpdateDownload()
}

// CancelUpdateDownload 取消进行中的下载。
func (b *Bridge) CancelUpdateDownload() service.UpdateStateDTO {
	return b.app.Service.CancelUpdateDownload()
}

// SetUpdateAutoCheck 开关启动后的静默检查并持久化。
func (b *Bridge) SetUpdateAutoCheck(enabled bool) service.UpdateStateDTO {
	return b.app.Service.SetUpdateAutoCheck(enabled)
}

// SkipUpdateVersion 记下不再提醒的版本号;空串表示跳过当前查到的最新版。
func (b *Bridge) SkipUpdateVersion(version string) service.UpdateStateDTO {
	return b.app.Service.SkipUpdateVersion(version)
}

// ClearSkippedUpdateVersion 撤销「跳过此版本」。
func (b *Bridge) ClearSkippedUpdateVersion() service.UpdateStateDTO {
	return b.app.Service.ClearSkippedUpdateVersion()
}

// RevealUpdateDownload 在系统文件管理器里打开安装包所在目录,返回是否已打开。
func (b *Bridge) RevealUpdateDownload() bool {
	path, _, err := b.app.Service.DownloadedUpdate()
	if err != nil {
		return false
	}
	if err := b.reveal(path); err != nil {
		b.app.Logger.Warn("打开安装包所在目录失败: %v", err)
		return false
	}
	return true
}

// InstallUpdate 对 Windows 安装包请求提权并启动静默安装,成功启动后退出应用;
// macOS 打开安装镜像,Linux 与免安装程序打开所在目录。
func (b *Bridge) InstallUpdate() (bool, error) {
	path, action, err := b.app.Service.DownloadedUpdate()
	if err != nil {
		return false, err
	}
	if action == update.ActionReveal {
		err = b.reveal(path)
	} else {
		err = b.runInstaller(path)
	}
	if err != nil {
		b.app.Logger.Warn("执行更新操作 %s 失败: %v", action, err)
		return false, err
	}
	return true, nil
}

func (b *Bridge) reveal(path string) error {
	wapp := application.Get()
	if wapp == nil {
		return errors.New("窗口系统不可用")
	}
	return wapp.Browser.OpenFile(filepath.Dir(path))
}
