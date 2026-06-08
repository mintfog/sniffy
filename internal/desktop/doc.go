// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package desktop 是桌面(Wails v3)transport:把 internal/service 的领域方法
// 作为 Wails Service 绑定给前端 JS 调用(Call.ByName),并用 Wails 事件做实时推送。
//
// 它与 internal/api(headless)平行,二者共享同一个 service。
package desktop
