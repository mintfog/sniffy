// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugins

import (
	"context"
	"net/http"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// PluginInfo 插件基本信息
type PluginInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Category    string `json:"category"`
}

// PluginConfig 插件配置
type PluginConfig struct {
	Enabled    bool                   `json:"enabled"`
	Priority   int                    `json:"priority"`
	Settings   map[string]interface{} `json:"settings"`
	Whitelist  []string              `json:"whitelist,omitempty"`
	Blacklist  []string              `json:"blacklist,omitempty"`
}

// InterceptContext 拦截上下文，包含请求处理所需的所有信息
type InterceptContext struct {
	Request        *http.Request
	Response       *http.Response
	Connection     types.Connection
	Timestamp      time.Time
	RequestBody    []byte
	ResponseBody   []byte
	RequestHeaders http.Header
	ResponseHeaders http.Header
	Metadata       map[string]interface{}
}

// InterceptResult 拦截结果
type InterceptResult struct {
	// Continue 是否继续处理
	Continue bool
	// Modified 是否修改了请求/响应
	Modified bool
	// Message 处理消息
	Message string
	// Error 错误信息
	Error error
	// Metadata 元数据
	Metadata map[string]interface{}
}

// Plugin 插件主接口
type Plugin interface {
	// GetInfo 获取插件信息
	GetInfo() PluginInfo

	// Initialize 初始化插件
	Initialize(ctx context.Context, config PluginConfig) error

	// Start 启动插件
	Start(ctx context.Context) error

	// Stop 停止插件
	Stop(ctx context.Context) error

	// IsEnabled 检查插件是否启用
	IsEnabled() bool

	// GetPriority 获取插件优先级（数字越小优先级越高）
	GetPriority() int
}

// RequestInterceptor 请求拦截器接口
type RequestInterceptor interface {
	Plugin
	
	// InterceptRequest 拦截请求
	InterceptRequest(ctx context.Context, interceptCtx *InterceptContext) (*InterceptResult, error)
}

// ResponseInterceptor 响应拦截器接口
type ResponseInterceptor interface {
	Plugin
	
	// InterceptResponse 拦截响应
	InterceptResponse(ctx context.Context, interceptCtx *InterceptContext) (*InterceptResult, error)
}

// ConnectionInterceptor 连接拦截器接口
type ConnectionInterceptor interface {
	Plugin
	
	// OnConnectionStart 连接开始时调用
	OnConnectionStart(ctx context.Context, conn types.Connection) error
	
	// OnConnectionEnd 连接结束时调用
	OnConnectionEnd(ctx context.Context, conn types.Connection, duration time.Duration) error
}

// DataProcessor 数据处理器接口
type DataProcessor interface {
	Plugin
	
	// ProcessData 处理数据
	ProcessData(ctx context.Context, data []byte, direction types.PacketDirection) ([]byte, error)
}

// WebSocketInterceptor WebSocket拦截器接口
type WebSocketInterceptor interface {
	Plugin
	
	// InterceptWebSocketMessage 拦截WebSocket消息
	InterceptWebSocketMessage(ctx context.Context, interceptCtx *WebSocketContext) (*InterceptResult, error)
}

// WebSocketContext WebSocket拦截上下文
type WebSocketContext struct {
	Connection     types.Connection
	Request        *http.Request           // WebSocket升级请求
	MessageType    WebSocketMessageType    // 消息类型
	Message        []byte                  // 消息内容
	Direction      WebSocketDirection      // 消息方向
	Timestamp      time.Time
	Metadata       map[string]interface{}
}

// WebSocketMessageType WebSocket消息类型
type WebSocketMessageType int

const (
	// TextMessage 文本消息
	TextMessage WebSocketMessageType = iota
	// BinaryMessage 二进制消息
	BinaryMessage
	// CloseMessage 关闭消息
	CloseMessage
	// PingMessage Ping消息
	PingMessage
	// PongMessage Pong消息
	PongMessage
)

// WebSocketDirection WebSocket消息方向
type WebSocketDirection int

const (
	// ClientToServer 客户端到服务器
	ClientToServer WebSocketDirection = iota
	// ServerToClient 服务器到客户端
	ServerToClient
)

// Logger 插件日志接口
type Logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Debug(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
}

// PluginAPI 插件API接口，提供给插件使用的应用能力
type PluginAPI interface {
	// GetLogger 获取日志器
	GetLogger(pluginName string) Logger
	
	// GetConfig 获取应用配置
	GetConfig() types.Config
	
	// SendNotification 发送通知
	SendNotification(title, message string) error
	
	// GetMetrics 获取指标
	GetMetrics() map[string]interface{}
	
	// StoreData 存储数据
	StoreData(key string, value interface{}) error
	
	// GetData 获取数据
	GetData(key string) (interface{}, error)
}

// PluginFactory 插件工厂函数类型
type PluginFactory func(api PluginAPI) Plugin

// PluginMetadata 插件元数据
type PluginMetadata struct {
	Info     PluginInfo   `json:"info"`
	Config   PluginConfig `json:"config"`
	FilePath string       `json:"file_path"`
	Factory  PluginFactory `json:"-"`
}

// InterceptorType 拦截器类型
type InterceptorType int

const (
	// TypeRequest 请求拦截器
	TypeRequest InterceptorType = iota
	// TypeResponse 响应拦截器
	TypeResponse
	// TypeConnection 连接拦截器
	TypeConnection
	// TypeData 数据处理器
	TypeData
)

// String 返回拦截器类型的字符串表示
func (t InterceptorType) String() string {
	switch t {
	case TypeRequest:
		return "request"
	case TypeResponse:
		return "response"
	case TypeConnection:
		return "connection"
	case TypeData:
		return "data"
	default:
		return "unknown"
	}
}