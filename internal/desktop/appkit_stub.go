// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && !darwin

package desktop

import "os"

// 仅 macOS 存在系统菜单栏自动追加项的问题，非 darwin 平台这些钩子为空操作（见 appkit_darwin.go）。

func suppressAutomaticMenuItems() {}

func pruneMenuTail(string, int) {}

// osPreferredLang 取机器语言（POSIX locale 环境变量），用于占位 UI（见 locale.go）。
// Windows/Linux 上 startupMacMenu 不生效，这里主要服务窗口描述；GUI 进程可能没有这些环境变量，
// 取不到时返回空串，由 normalizeLocale 回退到英文。
func osPreferredLang() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
