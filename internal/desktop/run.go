// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"fmt"
	"io/fs"
	"log"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/mintfog/sniffy/internal/app"
)

const mainWindowClass = "SniffyWebviewWindow"

// Run 装配并运行桌面应用，阻塞至退出；重复启动会唤醒已有主窗口并退出当前进程。
// dist 的根目录须包含前端构建产物的 index.html。
func Run(dist fs.FS) error {
	// 启动菜单使用机器语言，前端就绪后同步用户选择的语言。
	labels := labelsFor(uiLang())
	var mainWin *application.WebviewWindow
	windowReady := make(chan struct{})
	done := make(chan struct{})
	defer close(done)
	requestActivation := newWindowActivation(windowReady, done, func() { showWindow(mainWin) })

	// 单实例检查必须先于核心装配，避免重复启动争用代理端口或修改共享配置、缓存。
	allowExistingInstanceForeground()
	wapp := application.New(application.Options{
		Name:        "Sniffy",
		Description: labels.description,
		Windows: application.WindowsOptions{
			WndClass: mainWindowClass,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.mintfog.sniffy",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				requestActivation()
			},
		},
		// Wails 在 Windows 上优先读取图标资源 ID 3（见 build/windows/winres.json）。
		// Icon 供 macOS 使用，也供未嵌入资源的 Windows 开发构建回退使用。
		Icon: appIcon,
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(dist),
		},
		// WebView2 致命错误会直接退出进程，须在回调中同步落盘。
		ErrorHandler: func(err error) {
			log.Printf("[ERROR] Wails/WebView2: %v\n%s", err, debug.Stack())
			app.FlushLogs()
		},
	})

	cfg := app.DefaultConfig()
	cfg.Address, cfg.Port = app.ResolveListen(cfg.Address, cfg.Port)
	sniffyApp, err := app.Build(cfg, false)
	if err != nil {
		return fmt.Errorf("初始化失败: %w", err)
	}
	if err := sniffyApp.Start(); err != nil {
		_ = sniffyApp.Stop()
		return fmt.Errorf("启动引擎失败: %w", err)
	}
	bridge := New(sniffyApp)
	wapp.RegisterService(application.NewService(bridge))
	// 消息体字节流(音视频预览)挂在资源服务器上按 HTTP + Range 取。
	wapp.RegisterService(application.NewServiceWithOptions(
		&bodyRouteHandler{svc: sniffyApp.Service},
		application.ServiceOptions{Route: bodyRoute},
	))

	winOpts := application.WebviewWindowOptions{
		Name:             mainWindowName,
		Title:            "Sniffy",
		Width:            1280,
		Height:           820,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(17, 17, 23),
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
	}
	ApplyPlatformChrome(&winOpts)
	mainWin = wapp.Window.NewWithOptions(winOpts)
	// 就绪信号同时保证唤醒 goroutine 可安全读取 mainWin；页面重载时只关闭一次。
	markWindowReady := sync.OnceFunc(func() { close(windowReady) })
	mainWin.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		markWindowReady()
	})

	if runtime.GOOS == "darwin" {
		// 启动菜单避免默认英文菜单闪现；前端就绪后由 SetMenu 更新语言。
		suppressAutomaticMenuItems()
		menu, editLabel, editCount := startupMacMenu(labels)
		wapp.Menu.SetApplicationMenu(menu)
		pruneMenuTail(editLabel, editCount)
		wapp.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
			requestActivation()
		})
	}
	applyNativeWindowTitle(mainWin, "Sniffy")
	preserveMaximisedOnRestore(mainWin)

	// 在运行窗口前拦截关闭事件，保留从托盘唤醒的窗口。
	setupMainWindowLifecycle(mainWin, sniffyApp.Service)
	setupSystemTray(wapp, mainWin, labels)

	sniffyApp.Engine.SetPortalInstallHandler(func() {
		showWindow(mainWin)
		wapp.Event.Emit("certificate_install_requested")
	})
	defer sniffyApp.Engine.SetPortalInstallHandler(nil)

	return wapp.Run()
}
