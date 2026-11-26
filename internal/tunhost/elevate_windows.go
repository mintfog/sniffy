// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// shellExecuteInfo 即 SHELLEXECUTEINFOW。
type shellExecuteInfo struct {
	size       uint32
	mask       uint32
	hwnd       windows.Handle
	verb       *uint16
	file       *uint16
	parameters *uint16
	directory  *uint16
	show       int32
	instApp    windows.Handle
	idList     uintptr
	class      *uint16
	keyClass   windows.Handle
	hotKey     uint32
	icon       windows.Handle
	process    windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	seeMaskFlagNoUI       = 0x00000400
)

// ShellExecuteEx 一直阻塞到用户答复 UAC 提示，放到单独的 goroutine 里，结果经 exited 报告。
// UAC 拉起的进程由系统创建，取不到它的输出，只能报退出码。
func elevate(cmd []string, exited func(error)) error {
	file, err := windows.UTF16PtrFromString(cmd[0])
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd[1:]))
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	go func() {
		process, err := shellExecuteElevated(verb, file, params)
		if err != nil {
			exited(err)
			return
		}
		defer windows.CloseHandle(process)
		exited(waitExitCode(process))
	}()
	return nil
}

func shellExecuteElevated(verb, file, params *uint16) (windows.Handle, error) {
	// ShellExecuteEx 可能经由要求单线程套间的 shell 扩展执行，按文档先在本线程初始化 COM。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil {
		defer windows.CoUninitialize()
	}
	info := shellExecuteInfo{
		mask:       seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
		verb:       verb,
		file:       file,
		parameters: params,
		show:       windows.SW_HIDE,
	}
	info.size = uint32(unsafe.Sizeof(info))
	if ok, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return 0, ErrElevationCanceled
		}
		return 0, fmt.Errorf("%w: 请求管理员权限: %v", ErrElevationUnavailable, err)
	}
	if info.process == 0 {
		return 0, fmt.Errorf("%w: 请求管理员权限: 系统没有返回 helper 进程", ErrElevationUnavailable)
	}
	return info.process, nil
}

func waitExitCode(process windows.Handle) error {
	if _, err := windows.WaitForSingleObject(process, windows.INFINITE); err != nil {
		return fmt.Errorf("等待 helper: %w", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil {
		return fmt.Errorf("取 helper 的退出码: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("helper 以退出码 %d 结束", code)
	}
	return nil
}
