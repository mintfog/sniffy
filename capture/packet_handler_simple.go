// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/capture/processors"
	httpproc "github.com/mintfog/sniffy/capture/processors/http"
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

	// 每连接日志属于热路径,降为 Debug,避免高并发时日志写入拖慢连接处理。
	h.LogDebug("处理新连接: %s -> %s", info.RemoteAddr, info.LocalAddr)

	// 尝试检测协议类型
	protocol := h.registry.DetectProtocol(connection.GetReader(), h)
	h.LogDebug("检测到协议: %s", protocol)

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

// TransparentFilter 决定透明连接是否进抓包链路。client、target 是连接的 RemoteAddr 与 LocalAddr，
// 即应用的原始源地址与原始目标地址。err 只用于记录，是否抓取仍以 capture 为准。
type TransparentFilter func(client, target net.Addr) (capture bool, err error)

var transparentFilter atomic.Pointer[TransparentFilter]

// SetTransparentFilter 设置透明连接的过滤器，nil 表示全部抓取。只影响此后的新连接。
func SetTransparentFilter(f TransparentFilter) {
	if f == nil {
		transparentFilter.Store(nil)
	} else {
		transparentFilter.Store(&f)
	}
}

// HandleTransparent 处理一条透明连接，地址约定见 TCPListener.ServeTransparent。过滤器要抓取的连接与
// 代理入站连接一样套上限速后进抓包链路；其余原样转发到原始目标，不解密、不记录、不限速，与不开 TUN
// 时一样。
func (h *SimplePacketHandler) HandleTransparent(conn net.Conn) {
	serve := httpproc.ServeTransparent
	if h.captureTransparent(conn) {
		conn = wrapThrottleConn(conn)
	} else {
		h.LogDebug("透明连接 %s：所属应用不在抓取范围内，直通转发", conn.LocalAddr())
		serve = httpproc.RelayTransparent
	}
	connection := types.NewConnection(conn, h)
	defer connection.Close()

	h.LogDebug("处理透明连接: %s -> %s", conn.RemoteAddr(), conn.LocalAddr())
	err := serve(connection)
	var dialErr *httpproc.TunnelDialError
	switch {
	case err == nil:
	case errors.As(err, &dialErr):
		h.LogDebug("透明连接处理失败: %v", err)
	default:
		h.LogError("透明连接处理失败: %v", err)
	}
}

// captureTransparent 按过滤器判断透明连接是否进抓包链路，没有过滤器时全部抓取。
func (h *SimplePacketHandler) captureTransparent(conn net.Conn) bool {
	filter := transparentFilter.Load()
	if filter == nil {
		return true
	}
	capture, err := (*filter)(conn.RemoteAddr(), conn.LocalAddr())
	if err != nil {
		h.LogDebug("透明连接 %s：查找所属进程: %v", conn.LocalAddr(), err)
	}
	return capture
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
