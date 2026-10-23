// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && windows

package desktop

import (
	"sync/atomic"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

const wpfRestoreToMaximised = 0x0002

// Windows 的前台激活权须由新启动进程在通知已有实例前转交。
func allowExistingInstanceForeground() {
	hwnd := w32.FindWindowW(windows.StringToUTF16Ptr(mainWindowClass), nil)
	if hwnd == 0 {
		return
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil || pid == 0 {
		return
	}
	allowForeground := windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")
	allowForeground.Call(uintptr(pid))
}

// 任务栏恢复不经过 showWindow，需在窗口事件层保留最小化前的最大化状态。
func preserveMaximisedOnRestore(win application.Window) {
	var restoreMaximised atomic.Bool
	win.RegisterHook(events.Common.WindowMinimise, func(*application.WindowEvent) {
		restoreMaximised.Store(windowPlacementRestoresMaximised(win))
	})
	win.RegisterHook(events.Common.WindowUnMinimise, func(*application.WindowEvent) {
		if restoreMaximised.Swap(false) && !win.IsMaximised() {
			win.Maximise()
		}
	})
}

func restoreMinimisedWindow(win application.Window) {
	if win == nil || !win.IsMinimised() {
		return
	}

	if windowPlacementRestoresMaximised(win) {
		win.Maximise()
		return
	}
	win.Restore()
}

func windowPlacementRestoresMaximised(win application.Window) bool {
	placement := w32.WINDOWPLACEMENT{
		Length: uint32(unsafe.Sizeof(w32.WINDOWPLACEMENT{})),
	}
	hwnd := w32.HWND(uintptr(win.NativeWindow()))
	return hwnd != 0 && w32.GetWindowPlacement(hwnd, &placement) && placement.Flags&wpfRestoreToMaximised != 0
}
