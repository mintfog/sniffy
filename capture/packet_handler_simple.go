// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"fmt"
	"log"
	"net"
	"time"

	"github.com/mintfog/sniffy/capture/processors"
	"github.com/mintfog/sniffy/capture/types"
)

// SimplePacketHandler 新的简化数据包处理器
type SimplePacketHandler struct {
	config   types.Config
	logger   types.Logger
	registry *processors.Registry
}

// NewDefaultPacketHandler 创建新的简化数据包处理器
func NewDefaultPacketHandler(config types.Config) *SimplePacketHandler {
	return &SimplePacketHandler{
		config:   config,
		registry: processors.NewRegistry(),
	}
}

// SetLogger 设置日志器
func (h *SimplePacketHandler) SetLogger(logger types.Logger) {
	h.logger = logger
}

// 实现 types.Server 接口
func (h *SimplePacketHandler) GetConfig() types.Config {
	return h.config
}

func (h *SimplePacketHandler) LogInfo(format string, args ...any) {
	if h.logger != nil {
		h.logger.Info(format, args...)
	} else {
		log.Output(2, fmt.Sprintf("[INFO] "+format, args...))
	}
}

func (h *SimplePacketHandler) LogError(format string, args ...any) {
	if h.logger != nil {
		h.logger.Error(format, args...)
	} else {
		log.Output(2, fmt.Sprintf("[ERROR] "+format, args...))
	}
}

func (h *SimplePacketHandler) LogDebug(format string, args ...any) {
	if h.logger != nil {
		h.logger.Debug(format, args...)
	} else {
		log.Output(2, fmt.Sprintf("[DEBUG] "+format, args...))
	}
}

func (h *SimplePacketHandler) FormatDataPreview(data []byte) string {
	maxLen := 64
	if len(data) > maxLen {
		return fmt.Sprintf("%x... (truncated, total: %d bytes)", data[:maxLen], len(data))
	}
	return fmt.Sprintf("%x", data)
}

// 实现 types.PacketHandler 接口
func (h *SimplePacketHandler) HandleConnection(conn net.Conn, info *types.ConnectionInfo) {
	defer conn.Close()

	// 创建连接抽象
	connection := types.NewConnection(conn, h)
	defer connection.Close()

	h.LogInfo("处理新连接: %s -> %s", info.RemoteAddr, info.LocalAddr)

	// 尝试检测协议类型
	protocol := h.registry.DetectProtocol(connection.GetReader(), h)
	h.LogInfo("检测到协议: %s", protocol)

	// 获取处理器并处理连接
	processor := h.registry.GetProcessor(protocol, connection)
	if processor == nil {
		h.LogError("无法获取协议处理器: %s", protocol)
		return
	}

	// 处理协议
	if err := processor.Process(); err != nil {
		h.LogError("协议处理失败: %v", err)
	}
}

func (h *SimplePacketHandler) HandleError(err error, context string) {
	h.LogError("错误 [%s]: %v", context, err)
}

func (h *SimplePacketHandler) OnConnectionStart(conn net.Conn) error {
	h.LogDebug("连接开始: %s", conn.RemoteAddr())
	return nil
}

func (h *SimplePacketHandler) OnConnectionEnd(conn net.Conn, duration time.Duration) {
	h.LogDebug("连接结束: %s (持续时间: %v)", conn.RemoteAddr(), duration)
}
