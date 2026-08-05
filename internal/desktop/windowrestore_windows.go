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
)

const wpfRestoreToMaximised = 0x0002

// preserveMaximisedOnRestore 补偿 Wails alpha.97 的 Windows 恢复行为：SW_RESTORE 会忽略
// 最小化前的最大化状态。任务栏直接恢复不经过 showWindow，必须在窗口事件层修正。
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
	return hwnd != 0 && w32.GetWindowPlacement(hwnd, &placement) && placementRestoresMaximised(placement.Flags)
}

func placementRestoresMaximised(flags uint32) bool {
	return flags&wpfRestoreToMaximised != 0
}
