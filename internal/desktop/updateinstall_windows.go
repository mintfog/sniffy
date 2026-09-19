// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && windows

package desktop

import (
	"errors"
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// 安装包要求管理员权限,须通过 ShellExecute 的 runas 请求提权。
// 启动成功后再退出,安装程序会等待可执行文件解除占用。
func (b *Bridge) runInstaller(path string) error {
	wapp := application.Get()
	if wapp == nil {
		return errors.New("窗口系统不可用")
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	verb := windows.StringToUTF16Ptr("runas")
	args := windows.StringToUTF16Ptr("/S")
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("已取消安装授权,可再次点击「立即安装」")
		}
		return fmt.Errorf("启动安装程序失败: %w", err)
	}
	wapp.Quit()
	return nil
}
