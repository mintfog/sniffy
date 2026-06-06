// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package desktop 是桌面(Wails v2)transport:把 internal/service 的领域方法
// 绑定给前端 JS 调用,并用 Wails 事件做实时推送;同时提供原生菜单与系统托盘。
//
// 它与 internal/api(headless)平行,二者共享同一个 service。实现将在 P4 填充。
package desktop
