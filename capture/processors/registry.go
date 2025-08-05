// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package processors

import (
	"bufio"

	"github.com/mintfog/sniffy/capture/processors/http"
	"github.com/mintfog/sniffy/capture/processors/socks5"
	"github.com/mintfog/sniffy/capture/processors/tcp"
	"github.com/mintfog/sniffy/capture/types"
)

// Registry 处理器注册表
type Registry struct {
	factories map[string]types.ProcessorFactory
}

// NewRegistry 创建新的处理器注册表
func NewRegistry() *Registry {
	r := &Registry{
		factories: make(map[string]types.ProcessorFactory),
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
func (r *Registry) Register(protocol string, factory types.ProcessorFactory) {
	r.factories[protocol] = factory
}

// Unregister 注销处理器
func (r *Registry) Unregister(protocol string) {
	delete(r.factories, protocol)
}

// GetProcessor 根据协议名称获取处理器
func (r *Registry) GetProcessor(protocolName string, conn types.Connection) types.ProtocolProcessor {
	if factory, exists := r.factories[protocolName]; exists {
		return factory(conn)
	}
	// 默认返回TCP处理器
	return tcp.New(conn)
}

// DetectProtocol 根据连接数据检测协议类型
func (r *Registry) DetectProtocol(reader *bufio.Reader, server types.Server) string {
	// 协议检测：先读取第一个字节判断基础协议类型
	firstByte, err := reader.Peek(1)
	if err != nil {
		server.LogError("Failed to peek connection data: %v", err)
		return "TCP"
	}

	// 根据第一个字节确定协议类型
	switch firstByte[0] {
	// HTTP请求检测
	case MethodGet, MethodPost, MethodDelete, MethodOptions, MethodHead, MethodConnect:
		return "HTTP"
	// SOCKS5协议检测
	case SocksFive:
		return "SOCKS5"
	// TLS/SSL协议检测
	case TLSHandshake, TLSAlert, TLSAppData:
		// 进行更详细的TLS检测
		return r.detectTLSProtocol(reader, server)
	// SSH协议检测
	case SSHVersion:
		return r.detectSSHProtocol(reader, server)
	// FTP协议检测
	case FTPResponse:
		return r.detectNumericProtocol(reader, server)
	// MQTT协议检测
	case MQTTConnect:
		return "TCP"
	// 其他字节值需要更深入检测
	default:
		// RDP协议检测
		if firstByte[0] == RDPRequest {
			return "TCP"
		}
		// 如果前面都没匹配，进行更高级的协议检测
		return r.detectAdvancedProtocol(reader, server)
	}
}

// detectTLSProtocol 检测TLS协议
func (r *Registry) detectTLSProtocol(reader *bufio.Reader, server types.Server) string {
	// TLS/SSL协议检测
	server.LogInfo("检测到TLS/SSL协议")
	return "TCP" // 暂时使用TCP处理器处理TLS流量
}

// detectSSHProtocol 检测SSH协议
func (r *Registry) detectSSHProtocol(reader *bufio.Reader, server types.Server) string {
	// SSH协议的识别字符串：SSH-2.0-xxx 或 SSH-1.99-xxx
	sshHeader, err := reader.Peek(8) // 读取 "SSH-2.0-" 或 "SSH-1.99"
	if err != nil {
		return "TCP"
	}

	if len(sshHeader) >= 7 && string(sshHeader[:7]) == "SSH-2.0" {
		server.LogInfo("检测到SSH-2.0协议")
		return "TCP"
	} else if len(sshHeader) >= 8 && string(sshHeader[:8]) == "SSH-1.99" {
		server.LogInfo("检测到SSH-1.99协议")
		return "TCP"
	}

	return "TCP"
}

// detectNumericProtocol 检测以数字开头的协议（如FTP、SMTP等）
func (r *Registry) detectNumericProtocol(reader *bufio.Reader, server types.Server) string {
	// 读取更多字节来判断具体协议
	header, err := reader.Peek(12)
	if err != nil {
		return "TCP"
	}

	headerStr := string(header)

	// FTP协议检测
	if len(headerStr) >= 3 {
		switch headerStr[:3] {
		case "220", "230", "530":
			server.LogInfo("检测到FTP协议")
			return "TCP"
		case "250":
			// 可能是SMTP
			server.LogInfo("检测到可能的SMTP协议")
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
func (r *Registry) detectAdvancedProtocol(reader *bufio.Reader, server types.Server) string {
	// 读取更多字节进行高级协议检测
	header, err := reader.Peek(16)
	if err != nil {
		return "TCP"
	}

	// DNS协议检测（通常在UDP上，但也可能在TCP上）
	if len(header) >= 12 {
		// DNS查询头部检测
		server.LogInfo("进行高级协议检测")
	}

	// 默认返回TCP
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
