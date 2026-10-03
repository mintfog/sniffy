// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && !linux

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// applyNativeWindowTitle 在非 Linux 上为空操作：Windows/macOS 的 SetTitle 不受
// Frameless 短路影响，任务栏标题走各自原生路径即可。
func applyNativeWindowTitle(application.Window, string) {}
