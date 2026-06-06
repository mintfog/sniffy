// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"context"

	"github.com/mintfog/sniffy/internal/flow"
)

// Hook 是所有插件(Go 原生层与 goja JS 层)共享的基础接口。
type Hook interface {
	// Name 插件标识。
	Name() string
	// Priority 优先级,数值越小越先执行。
	Priority() int
	// Enabled 是否启用。
	Enabled() bool
	// Match 判断该插件是否作用于给定 URL(白/黑名单门控)。返回 false 则跳过。
	Match(url string) bool
}

// RequestHook 在请求阶段被调用。插件就地修改 f.Request,并返回处置。
type RequestHook interface {
	Hook
	OnRequest(ctx context.Context, f *flow.Flow) flow.Decision
}

// ResponseHook 在响应阶段被调用。插件就地修改 f.Response,并返回处置。
type ResponseHook interface {
	Hook
	OnResponse(ctx context.Context, f *flow.Flow) flow.Decision
}

// WSHook 在每条 WebSocket 消息上被调用。插件可就地修改 m.Data。
type WSHook interface {
	Hook
	OnWebSocketMessage(ctx context.Context, m *flow.WSMessage) flow.Decision
}

// ConnHook 在连接开始/结束时被调用(可选)。
type ConnHook interface {
	Hook
	OnConnect(ctx context.Context, connID, remoteAddr string)
	OnDisconnect(ctx context.Context, connID string)
}
