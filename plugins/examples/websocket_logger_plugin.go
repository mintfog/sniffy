// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mintfog/sniffy/plugins"
)

// WebSocketLoggerPlugin WebSocket消息日志插件
type WebSocketLoggerPlugin struct {
	BasePlugin
	api           plugins.PluginAPI
	logger        plugins.Logger
	enableLogging bool
	logLevel      string
}

// NewWebSocketLoggerPlugin 创建WebSocket日志插件
func NewWebSocketLoggerPlugin(api plugins.PluginAPI) plugins.Plugin {
	return &WebSocketLoggerPlugin{
		BasePlugin: BasePlugin{
			info: plugins.PluginInfo{
				Name:        "websocket_logger",
				Version:     "1.0.0",
				Description: "记录WebSocket消息的插件",
				Author:      "sniffy team",
				Category:    "monitoring",
			},
			enabled:  true,
			priority: 50,
		},
		api:    api,
		logger: api.GetLogger("websocket_logger"),
	}
}

// Initialize 初始化插件
func (p *WebSocketLoggerPlugin) Initialize(ctx context.Context, config plugins.PluginConfig) error {
	p.config = config
	
	// 读取配置
	if enabled, ok := config.Settings["enabled"].(bool); ok {
		p.enableLogging = enabled
	} else {
		p.enableLogging = true
	}
	
	if level, ok := config.Settings["log_level"].(string); ok {
		p.logLevel = level
	} else {
		p.logLevel = "info"
	}
	
	p.logger.Info("WebSocket日志插件初始化完成，启用状态: %v，日志级别: %s", p.enableLogging, p.logLevel)
	return nil
}

// InterceptWebSocketMessage 拦截WebSocket消息
func (p *WebSocketLoggerPlugin) InterceptWebSocketMessage(ctx context.Context, interceptCtx *plugins.WebSocketContext) (*plugins.InterceptResult, error) {
	if !p.enableLogging {
		return &plugins.InterceptResult{Continue: true}, nil
	}

	// 记录消息信息
	direction := "客户端->服务器"
	if interceptCtx.Direction == plugins.ServerToClient {
		direction = "服务器->客户端"
	}

	messageTypeStr := p.getMessageTypeString(interceptCtx.MessageType)
	
	// 构建日志消息
	logMsg := fmt.Sprintf("WebSocket消息 [%s] %s: 类型=%s, 大小=%d字节",
		direction,
		interceptCtx.Request.Host,
		messageTypeStr,
		len(interceptCtx.Message))
	
	// 根据日志级别记录
	switch strings.ToLower(p.logLevel) {
	case "debug":
		// 在debug级别显示消息内容（前100个字符）
		content := string(interceptCtx.Message)
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		p.logger.Debug("%s, 内容: %s", logMsg, content)
	case "info":
		p.logger.Info(logMsg)
	case "warn":
		p.logger.Warn(logMsg)
	}

	// 将消息信息存储到元数据中
	interceptCtx.Metadata["logged_at"] = time.Now()
	interceptCtx.Metadata["direction"] = direction
	interceptCtx.Metadata["message_type"] = messageTypeStr
	interceptCtx.Metadata["size"] = len(interceptCtx.Message)

	// 如果是JSON格式的文本消息，尝试美化格式
	if interceptCtx.MessageType == plugins.TextMessage && p.logLevel == "debug" {
		if p.isJSONMessage(interceptCtx.Message) {
			beautified, err := p.beautifyJSON(interceptCtx.Message)
			if err == nil {
				p.logger.Debug("JSON消息格式化:\n%s", beautified)
			}
		}
	}

	return &plugins.InterceptResult{
		Continue: true,
		Modified: false,
		Message:  "WebSocket消息已记录",
	}, nil
}

// getMessageTypeString 获取消息类型字符串
func (p *WebSocketLoggerPlugin) getMessageTypeString(messageType plugins.WebSocketMessageType) string {
	switch messageType {
	case plugins.TextMessage:
		return "文本"
	case plugins.BinaryMessage:
		return "二进制"
	case plugins.CloseMessage:
		return "关闭"
	case plugins.PingMessage:
		return "Ping"
	case plugins.PongMessage:
		return "Pong"
	default:
		return "未知"
	}
}

// isJSONMessage 检查是否为JSON消息
func (p *WebSocketLoggerPlugin) isJSONMessage(message []byte) bool {
	var js json.RawMessage
	return json.Unmarshal(message, &js) == nil
}

// beautifyJSON 美化JSON格式
func (p *WebSocketLoggerPlugin) beautifyJSON(message []byte) (string, error) {
	var obj interface{}
	if err := json.Unmarshal(message, &obj); err != nil {
		return "", err
	}
	
	beautified, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "", err
	}
	
	return string(beautified), nil
}

// Start 启动插件
func (p *WebSocketLoggerPlugin) Start(ctx context.Context) error {
	p.logger.Info("WebSocket日志插件已启动")
	return nil
}

// Stop 停止插件
func (p *WebSocketLoggerPlugin) Stop(ctx context.Context) error {
	p.logger.Info("WebSocket日志插件已停止")
	return nil
}

// 确保实现了WebSocketInterceptor接口
var _ plugins.WebSocketInterceptor = (*WebSocketLoggerPlugin)(nil)
