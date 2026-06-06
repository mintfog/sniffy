// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package api 是 headless(无界面服务器)transport:对外提供 HTTP REST API
// 与 gorilla/websocket 实时推送,全部委托给 internal/service。
//
// 它承接历史上 web_api 插件的路由职责,但 web_api 不再是"插件",
// 而是 service 之上的一个薄传输层。实现将在 P2/P3 填充。
package api
