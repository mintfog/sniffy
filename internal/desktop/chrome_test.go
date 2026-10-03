// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"runtime"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestApplyPlatformChrome(t *testing.T) {
	var opts application.WebviewWindowOptions
	ApplyPlatformChrome(&opts)

	switch runtime.GOOS {
	case "windows", "linux":
		if !opts.Frameless {
			t.Fatalf("%s 窗口应启用无边框模式", runtime.GOOS)
		}
	case "darwin":
		if opts.Mac.TitleBar != application.MacTitleBarHiddenInset {
			t.Fatalf("macOS 标题栏模式 = %v，期望 HiddenInset", opts.Mac.TitleBar)
		}
		if opts.Frameless {
			t.Fatal("macOS 窗口不应启用无边框模式")
		}
	default:
		t.Fatalf("未覆盖的平台: %s", runtime.GOOS)
	}
}
