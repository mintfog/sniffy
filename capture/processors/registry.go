// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package processors

import (
	"bufio"

	"github.com/f-dong/sniffy/capture/core"
	"github.com/f-dong/sniffy/capture/processors/http"
	"github.com/f-dong/sniffy/capture/processors/socks5"
	"github.com/f-dong/sniffy/capture/processors/tcp"
)

// Registry 处理器注册表
type Registry struct {
	factories map[string]core.ProcessorFactory
}

// NewRegistry 创建新的处理器注册表
func NewRegistry() *Registry {
	r := &Registry{
		factories: make(map[string]core.ProcessorFactory),
	}

	// 注册默认处理器
	r.RegisterDefaults()

	return r
}

// RegisterDefaults 注册默认处理器
func (r *Registry) RegisterDefaults() {
	r.Register("HTTP", http.New)
	r.Register("SOCKS5", socks5.New)
	r.Register("TCP", tcp.New)
}

// Register 注册处理器工厂
func (r *Registry) Register(protocol string, factory core.ProcessorFactory) {
	r.factories[protocol] = factory
}

// Unregister 注销处理器
func (r *Registry) Unregister(protocol string) {
	delete(r.factories, protocol)
}

// GetProcessor 根据协议名称获取处理器
func (r *Registry) GetProcessor(protocol string, conn core.Connection) core.ProtocolProcessor {
	if factory, exists := r.factories[protocol]; exists {
		return factory(conn)
	}
	// 默认返回TCP处理器
	return tcp.New(conn)
}

// DetectProtocol 根据连接数据检测协议类型
func (r *Registry) DetectProtocol(reader *bufio.Reader, server core.Server) string {
	// 协议检测：先读取第一个字节判断基础协议类型
	firstByte, err := reader.Peek(1)
	if err != nil {
		server.LogError("Failed to peek connection data: %v", err)
		return "TCP"
	}

	// 根据第一个字节确定协议类型
	switch firstByte[0] {
	case core.MethodGet, core.MethodPost, core.MethodDelete, core.MethodOptions, core.MethodHead, core.MethodConnect:
		// HTTP协议：可能需要进一步检测完整的请求行
		return "HTTP"
	case core.SocksFive:
		// SOCKS5协议：第一个字节是版本号
		return "SOCKS5"
	case core.TLSHandshake, core.TLSAlert, core.TLSAppData:
		// TLS/SSL协议
		if server.GetConfig().IsLoggingEnabled() {
			server.LogInfo("Detected TLS/SSL protocol (byte: 0x%02x)", firstByte[0])
		}
		return "TCP" // 暂时使用TCP处理
	case core.SSHVersion:
		// SSH协议，需要进一步验证 "SSH-2.0" 或 "SSH-1.99"
		return r.detectSSHProtocol(reader, server)
	case core.FTPResponse:
		// FTP响应或其他以数字开头的协议，需要进一步检测
		return r.detectNumericProtocol(reader, server)
	case core.MQTTConnect:
		// MQTT协议
		if server.GetConfig().IsLoggingEnabled() {
			server.LogInfo("Detected potential MQTT protocol")
		}
		return "TCP"
	case core.RDPRequest:
		// RDP协议
		if server.GetConfig().IsLoggingEnabled() {
			server.LogInfo("Detected potential RDP protocol")
		}
		return "TCP"
	default:
		// 未知协议，进行高级检测或使用默认TCP处理器
		if r.needsAdvancedDetection(firstByte[0]) {
			return r.detectAdvancedProtocol(reader, server)
		} else {
			return "TCP"
		}
	}
}

// detectSSHProtocol 检测SSH协议
func (r *Registry) detectSSHProtocol(reader *bufio.Reader, server core.Server) string {
	// SSH协议的识别字符串：SSH-2.0-xxx 或 SSH-1.99-xxx
	sshHeader, err := reader.Peek(8) // 读取 "SSH-2.0-" 或 "SSH-1.99"
	if err != nil {
		return "TCP"
	}

	if len(sshHeader) >= 7 && string(sshHeader[:7]) == "SSH-2.0" {
		if server.GetConfig().IsLoggingEnabled() {
			server.LogInfo("Detected SSH-2.0 protocol")
		}
		return "TCP"
	} else if len(sshHeader) >= 8 && string(sshHeader[:8]) == "SSH-1.99" {
		if server.GetConfig().IsLoggingEnabled() {
			server.LogInfo("Detected SSH-1.99 protocol")
		}
		return "TCP"
	}

	// 不是SSH协议，返回TCP处理器
	return "TCP"
}

// detectNumericProtocol 检测以数字开头的协议（如FTP、SMTP等）
func (r *Registry) detectNumericProtocol(reader *bufio.Reader, server core.Server) string {
	// 读取更多字节来判断具体协议
	header, err := reader.Peek(12)
	if err != nil {
		return "TCP"
	}

	headerStr := string(header)

	// FTP协议检测
	if len(header) >= 3 {
		if headerStr[:3] == "220" { // FTP服务就绪
			if server.GetConfig().IsLoggingEnabled() {
				server.LogInfo("Detected FTP protocol (220 response)")
			}
			return "TCP"
		}
		if headerStr[:3] == "230" { // FTP用户登录成功
			if server.GetConfig().IsLoggingEnabled() {
				server.LogInfo("Detected FTP protocol (230 response)")
			}
			return "TCP"
		}
	}

	// SMTP协议检测
	if len(header) >= 3 && headerStr[:3] == "220" {
		// 进一步检查是否包含SMTP特征
		if len(header) >= 8 && (headerStr[4:8] == "SMTP" || headerStr[4:8] == "smtp") {
			if server.GetConfig().IsLoggingEnabled() {
				server.LogInfo("Detected SMTP protocol")
			}
			return "TCP"
		}
	}

	return "TCP"
}

// needsAdvancedDetection 判断是否需要高级检测
func (r *Registry) needsAdvancedDetection(firstByte byte) bool {
	// 对于某些字节值，需要进行更复杂的协议检测
	switch firstByte {
	case 0x00, 0x01, 0x02, 0x04: // 一些二进制协议的开始字节
		return true
	default:
		return false
	}
}

// detectAdvancedProtocol 高级协议检测
func (r *Registry) detectAdvancedProtocol(reader *bufio.Reader, server core.Server) string {
	// 读取更多字节进行高级协议检测
	header, err := reader.Peek(16)
	if err != nil {
		return "TCP"
	}

	if server.GetConfig().IsLoggingEnabled() {
		server.LogInfo("Advanced protocol detection for header: %v", header)
	}

	// 这里可以添加更多的协议检测逻辑
	// 例如检测二进制协议、自定义协议等

	return "TCP"
}

// GetRegisteredProtocols 获取已注册的协议列表
func (r *Registry) GetRegisteredProtocols() []string {
	protocols := make([]string, 0, len(r.factories))
	for protocol := range r.factories {
		protocols = append(protocols, protocol)
	}
	return protocols
}
