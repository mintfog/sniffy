// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ApplyPlatformChrome 在创建窗口前设置与前端标题栏配套的窗口选项。
// macOS 的透明标题栏让前端提供主题底色，同时保留系统窗口按钮。
func ApplyPlatformChrome(opts *application.WebviewWindowOptions) {
	switch runtime.GOOS {
	case "windows", "linux":
		opts.Frameless = true
	case "darwin":
		opts.Mac.TitleBar = application.MacTitleBarHiddenInset
		opts.Mac.TitleBar.ToolbarStyle = application.MacToolbarStyleUnifiedCompact
	}
}
