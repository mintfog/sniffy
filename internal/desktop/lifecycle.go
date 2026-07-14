// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/mintfog/sniffy/internal/service"
)

// setupMainWindowLifecycle 拦截主窗关闭:按 RunInBackground 决定隐藏到托盘还是彻底退出。
//
// 只在「用户点红叉/系统关闭按钮」路径生效——Wails 的 windowShouldClose 只在这条路径
// 上派发 WindowClosing;程序化 Close()、菜单/托盘/Cmd+Q 走的 NSApplication terminate
// 全部绕过本钩子,故不会误拦截显式退出意图。
//
// 无论开关状态都调用 Cancel():阻止 Wails 内建监听器把窗口 markAsDestroyed。销毁后
// 窗口无法再被 Show() 找回,而用户重新打开时期望的正是「同一个窗口」而不是新建。
func setupMainWindowLifecycle(mainWin application.Window, svc *service.Service) {
	mainWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		if svc.Config().RunInBackground {
			mainWin.Hide()
			return
		}
		if a := application.Get(); a != nil {
			a.Quit()
		}
	})
}
