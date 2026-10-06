// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && windows

package desktop

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// 安装包要求管理员权限,须通过 ShellExecute 的 runas 请求提权。
// 应用保持运行，由安装器在用户确认后关闭并替换。
func (b *Bridge) runInstaller(path string) error {
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		return fmt.Errorf("获取更新发起进程信息失败: %w", err)
	}
	verb := windows.StringToUTF16Ptr("runas")
	// 创建时间与 PID 共同标识免安装版的发起实例，避免 PID 复用导致误关闭。
	args := windows.StringToUTF16Ptr(fmt.Sprintf("/S /UPDATEPID=%d /UPDATESTARTLOW=%d /UPDATESTARTHIGH=%d",
		windows.GetCurrentProcessId(), created.LowDateTime, created.HighDateTime))
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("已取消安装授权,可再次点击「立即安装」")
		}
		return fmt.Errorf("启动安装程序失败: %w", err)
	}
	return nil
}
