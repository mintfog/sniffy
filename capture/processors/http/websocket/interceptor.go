// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"context"
	"net/http"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/plugins"
)

// MessageInterceptor WebSocket消息拦截器
type MessageInterceptor struct {
	hookExecutor *plugins.HookExecutor
	logger       types.Logger
	request      *http.Request
}

// NewMessageInterceptor 创建WebSocket消息拦截器
func NewMessageInterceptor(hookExecutor *plugins.HookExecutor, logger types.Logger, request *http.Request) *MessageInterceptor {
	return &MessageInterceptor{
		hookExecutor: hookExecutor,
		logger:       logger,
		request:      request,
	}
}

// InterceptMessage 拦截WebSocket消息
func (mi *MessageInterceptor) InterceptMessage(
	message []byte,
	messageType plugins.WebSocketMessageType,
	direction plugins.WebSocketDirection,
	conn types.Connection,
) ([]byte, error) {
	if mi.hookExecutor == nil {
		return message, nil
	}

	// 创建WebSocket拦截上下文
	wsCtx := &plugins.WebSocketContext{
		Connection:  conn,
		Request:     mi.request,
		MessageType: messageType,
		Message:     message,
		Direction:   direction,
		Timestamp:   time.Now(),
		Metadata:    make(map[string]interface{}),
	}

	// 执行WebSocket消息拦截钩子
	ctx := context.Background()
	result, err := mi.hookExecutor.ExecuteWebSocketMessageHooks(ctx, wsCtx)
	if err != nil {
		mi.logger.Error("执行WebSocket消息钩子失败: %v", err)
		return message, err
	}

	// 处理拦截结果
	if result != nil {
		if !result.Continue {
			mi.logger.Info("WebSocket消息被插件终止: %s", result.Message)
			return nil, &InterceptError{Message: result.Message}
		}

		if result.Modified {
			mi.logger.Debug("WebSocket消息已被插件修改: %s", result.Message)
			// 如果消息被修改，从上下文中获取修改后的消息
			return wsCtx.Message, nil
		}
	}

	return message, nil
}

// InterceptError WebSocket拦截错误
type InterceptError struct {
	Message string
}

func (e *InterceptError) Error() string {
	return e.Message
}

// GetMessageType 根据WebSocket库的消息类型转换为插件系统的消息类型
func GetMessageType(wsMessageType int) plugins.WebSocketMessageType {
	switch wsMessageType {
	case 1: // websocket.TextMessage
		return plugins.TextMessage
	case 2: // websocket.BinaryMessage
		return plugins.BinaryMessage
	case 8: // websocket.CloseMessage
		return plugins.CloseMessage
	case 9: // websocket.PingMessage
		return plugins.PingMessage
	case 10: // websocket.PongMessage
		return plugins.PongMessage
	default:
		return plugins.BinaryMessage // 默认为二进制消息
	}
}

// GetWebSocketMessageType 根据插件系统的消息类型转换为WebSocket库的消息类型
func GetWebSocketMessageType(messageType plugins.WebSocketMessageType) int {
	switch messageType {
	case plugins.TextMessage:
		return 1 // websocket.TextMessage
	case plugins.BinaryMessage:
		return 2 // websocket.BinaryMessage
	case plugins.CloseMessage:
		return 8 // websocket.CloseMessage
	case plugins.PingMessage:
		return 9 // websocket.PingMessage
	case plugins.PongMessage:
		return 10 // websocket.PongMessage
	default:
		return 2 // 默认为二进制消息
	}
}
