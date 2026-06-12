// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && !darwin

package desktop

// 仅 macOS 存在系统菜单栏自动追加项的问题，非 darwin 平台这些钩子为空操作（见 appkit_darwin.go）。

func suppressAutomaticMenuItems() {}

func pruneMenuTail(string, int) {}
