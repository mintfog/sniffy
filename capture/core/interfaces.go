// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"bufio"
	"net"
	"time"
)

// ProtocolProcessor 协议处理器接口
type ProtocolProcessor interface {
	Process() error
	GetProtocolName() string
}

// Connection 连接接口，抽象化连接操作
type Connection interface {
	// GetConn 获取原始网络连接
	GetConn() net.Conn

	// GetReader 获取缓冲读取器
	GetReader() *bufio.Reader

	// GetWriter 获取缓冲写入器
	GetWriter() *bufio.Writer

	// GetServer 获取服务器实例
	GetServer() Server

	// Close 关闭连接
	Close() error
}

// Server 服务器接口，提供配置和日志功能
type Server interface {
	// GetConfig 获取配置
	GetConfig() Config

	// LogInfo 记录信息日志
	LogInfo(msg string, args ...interface{})

	// LogError 记录错误日志
	LogError(msg string, args ...interface{})

	// LogDebug 记录调试日志
	LogDebug(msg string, args ...interface{})

	// FormatDataPreview 格式化数据预览
	FormatDataPreview(data []byte) string
}

// Config 配置接口
type Config interface {
	// GetAddress 获取监听地址
	GetAddress() string

	// GetPort 获取监听端口
	GetPort() int

	// GetBufferSize 获取缓冲区大小
	GetBufferSize() int

	// GetReadTimeout 获取读取超时
	GetReadTimeout() time.Duration

	// GetWriteTimeout 获取写入超时
	GetWriteTimeout() time.Duration

	// IsLoggingEnabled 是否启用日志
	IsLoggingEnabled() bool

	// GetThreads 获取线程数
	GetThreads() int
}

// Logger 日志接口
type Logger interface {
	// Info 信息日志
	Info(msg string, args ...interface{})

	// Error 错误日志
	Error(msg string, args ...interface{})

	// Debug 调试日志
	Debug(msg string, args ...interface{})

	// Warn 警告日志
	Warn(msg string, args ...interface{})
}

// ProcessorFactory 处理器工厂函数类型
type ProcessorFactory func(conn Connection) ProtocolProcessor

// PacketHandler 数据包处理接口
type PacketHandler interface {
	// HandleConnection 处理TCP连接
	HandleConnection(conn net.Conn, info *ConnectionInfo)

	// HandleError 处理错误
	HandleError(err error, context string)

	// OnConnectionStart 连接开始时的回调
	OnConnectionStart(conn net.Conn) error

	// OnConnectionEnd 连接结束时的回调
	OnConnectionEnd(conn net.Conn, duration time.Duration)
}

// ConnectionInfo 连接信息
type ConnectionInfo struct {
	// LocalAddr 本地地址
	LocalAddr net.Addr

	// RemoteAddr 远程地址
	RemoteAddr net.Addr

	// StartTime 连接开始时间
	StartTime time.Time

	// BufferSize 缓冲区大小
	BufferSize int

	// ReadTimeout 读取超时
	ReadTimeout time.Duration

	// WriteTimeout 写入超时
	WriteTimeout time.Duration
}

// PacketInfo 数据包信息
type PacketInfo struct {
	// ConnectionInfo 连接信息
	Connection *ConnectionInfo

	// Timestamp 时间戳
	Timestamp time.Time

	// Size 数据包大小
	Size int

	// Direction 数据方向 (inbound/outbound)
	Direction PacketDirection

	// SequenceNumber 序列号 (用于TCP流重组)
	SequenceNumber uint32
}

// PacketDirection 数据包方向
type PacketDirection int

const (
	// DirectionInbound 入站数据
	DirectionInbound PacketDirection = iota

	// DirectionOutbound 出站数据
	DirectionOutbound
)

// String 返回方向的字符串表示
func (d PacketDirection) String() string {
	switch d {
	case DirectionInbound:
		return "inbound"
	case DirectionOutbound:
		return "outbound"
	default:
		return "unknown"
	}
}
