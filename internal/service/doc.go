// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package service 是整个应用的唯一真相源(single source of truth):
// 会话存储、统计、拦截规则、录制状态、配置、证书、插件管理与断点。
//
// 它消费 core.EventBus 的事件、维护状态,并向两种 transport
// (internal/api 的 headless HTTP+WS、internal/desktop 的 Wails 绑定)
// 暴露领域方法。两种 transport 都是本包的薄壳,逻辑只在这里。
//
// 实现将在 P2 起逐步填充(替换历史 web_api 插件中的桩实现)。
package service
