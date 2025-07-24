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

	"github.com/f-dong/sniffy/capture/core"
	"github.com/f-dong/sniffy/capture/processors"
)

// SimplePacketHandler 新的简化数据包处理器
type SimplePacketHandler struct {
	config   core.Config
	logger   core.Logger
	registry *processors.Registry
}

// NewDefaultPacketHandler 创建新的简化数据包处理器
func NewDefaultPacketHandler(config core.Config) *SimplePacketHandler {
	return &SimplePacketHandler{
		config:   config,
		registry: processors.NewRegistry(),
	}
}

// SetLogger 设置日志器
func (h *SimplePacketHandler) SetLogger(logger core.Logger) {
	h.logger = logger
}

// 实现 core.Server 接口
func (h *SimplePacketHandler) GetConfig() core.Config {
	return h.config
}

func (h *SimplePacketHandler) LogInfo(format string, args ...interface{}) {
	if h.logger != nil {
		h.logger.Info(format, args...)
	} else {
		log.Printf("[INFO] "+format, args...)
	}
}

func (h *SimplePacketHandler) LogError(format string, args ...interface{}) {
	if h.logger != nil {
		h.logger.Error(format, args...)
	} else {
		log.Printf("[ERROR] "+format, args...)
	}
}

func (h *SimplePacketHandler) LogDebug(format string, args ...interface{}) {
	if h.logger != nil {
		h.logger.Debug(format, args...)
	} else {
		log.Printf("[DEBUG] "+format, args...)
	}
}

func (h *SimplePacketHandler) FormatDataPreview(data []byte) string {
	if len(data) == 0 {
		return "<empty>"
	}

	preview := ""
	for i, b := range data {
		if i >= 32 {
			preview += "..."
			break
		}
		if b >= 32 && b <= 126 {
			preview += string(b)
		} else {
			preview += fmt.Sprintf("\\x%02x", b)
		}
	}
	return preview
}

// HandleConnection 实现 PacketHandler 接口
func (h *SimplePacketHandler) HandleConnection(conn net.Conn, info *core.ConnectionInfo) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	localAddr := conn.LocalAddr().String()

	if h.config.IsLoggingEnabled() {
		h.LogInfo("Handling connection: %s -> %s", remoteAddr, localAddr)
	}

	// 设置连接超时
	if err := conn.SetReadDeadline(time.Now().Add(h.config.GetReadTimeout())); err != nil {
		h.HandleError(fmt.Errorf("failed to set read deadline: %w", err), "HandleConnection")
		return
	}
	if err := conn.SetWriteDeadline(time.Now().Add(h.config.GetWriteTimeout())); err != nil {
		h.HandleError(fmt.Errorf("failed to set write deadline: %w", err), "HandleConnection")
		return
	}

	// 创建连接实例
	connection := core.NewConnection(conn, h)

	// 协议检测
	protocol := h.registry.DetectProtocol(connection.GetReader(), h)

	if h.config.IsLoggingEnabled() {
		h.LogInfo("Detected protocol: %s for connection %s", protocol, remoteAddr)
	}

	// 获取处理器并处理连接
	processor := h.registry.GetProcessor(protocol, connection)
	err := processor.Process()
	if err != nil {
		h.HandleError(fmt.Errorf("protocol processing failed: %w", err), "HandleConnection")
		return
	}

	if h.config.IsLoggingEnabled() {
		h.LogInfo("Connection %s processed successfully", remoteAddr)
	}
}

func (h *SimplePacketHandler) HandleError(err error, context string) {
	h.LogError("Error in %s: %v", context, err)
}

func (h *SimplePacketHandler) OnConnectionStart(conn net.Conn) error {
	if h.config.IsLoggingEnabled() {
		h.LogInfo("Connection started: %s", conn.RemoteAddr().String())
	}
	return nil
}

func (h *SimplePacketHandler) OnConnectionEnd(conn net.Conn, duration time.Duration) {
	if h.config.IsLoggingEnabled() {
		h.LogInfo("Connection ended: %s, duration: %v", conn.RemoteAddr().String(), duration)
	}
}
