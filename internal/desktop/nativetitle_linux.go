// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && linux

package desktop

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
*/
import "C"

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// applyNativeWindowTitle 把标题写进 GTK 窗口，供任务栏/切换器显示。
//
// Wails v3.0.0-beta.22 的 linuxWebviewWindow.setTitle 在 Frameless 时会跳过
// gtk_window_set_title；Linux 无边框自绘后 WebviewWindowOptions.Title 因此到不了
// 原生层。这里绕过该短路，直接设置。
//
// 主窗在 app.Run 前创建时 NativeWindow 尚不可用，且此时 InvokeSync 会因 a.impl
// 未初始化而 panic，故只挂一次性 WindowShow 钩子，等初始化完成后再在 GTK 主线程设置。
func applyNativeWindowTitle(win application.Window, title string) {
	if win == nil || title == "" {
		return
	}
	// app.Run 前句柄一定为 nil；先探测再决定是否 InvokeSync，避免启动 panic。
	if win.NativeWindow() == nil {
		var unhook func()
		unhook = win.RegisterHook(events.Common.WindowShow, func(*application.WindowEvent) {
			setNativeWindowTitle(win, title)
			if unhook != nil {
				unhook()
				unhook = nil
			}
		})
		return
	}
	setNativeWindowTitle(win, title)
}

// setNativeWindowTitle 在 GTK 主线程设置标题。句柄尚未创建时返回 false。
func setNativeWindowTitle(win application.Window, title string) bool {
	ok := false
	application.InvokeSync(func() {
		native := win.NativeWindow()
		if native == nil {
			return
		}
		ctitle := C.CString(title)
		defer C.free(unsafe.Pointer(ctitle))
		C.gtk_window_set_title((*C.GtkWindow)(native), ctitle)
		ok = true
	})
	return ok
}
