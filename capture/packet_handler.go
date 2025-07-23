// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"time"
)

// ConnPeer 连接对等体，包装连接和缓冲区
type ConnPeer struct {
	server *DefaultPacketHandler
	conn   net.Conn
	writer *bufio.Writer
	reader *bufio.Reader
}

// ProtocolProcessor 协议处理器接口
type ProtocolProcessor interface {
	Process() error
	GetProtocolName() string
}

// DefaultPacketHandler 实现 PacketHandler 接口的默认实现
// 该实现可用于通用抓包和调试
type DefaultPacketHandler struct {
	config Config
	logger Logger
}

// NewDefaultPacketHandler 创建新的默认数据包处理器
func NewDefaultPacketHandler(config Config) *DefaultPacketHandler {
	return &DefaultPacketHandler{
		config: config,
	}
}

// SetLogger 设置日志器
func (h *DefaultPacketHandler) SetLogger(logger Logger) {
	h.logger = logger
}

// HandleConnection 处理TCP连接
func (h *DefaultPacketHandler) HandleConnection(conn net.Conn, info *ConnectionInfo) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	localAddr := conn.LocalAddr().String()

	if h.config.IsLoggingEnabled() {
		h.logInfo("Handling connection: %s -> %s", remoteAddr, localAddr)
	}

	// 设置连接超时
	conn.SetReadDeadline(time.Now().Add(h.config.GetReadTimeout()))
	conn.SetWriteDeadline(time.Now().Add(h.config.GetWriteTimeout()))

	// 创建缓冲读写器
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// 协议检测：先读取第一个字节判断基础协议类型
	firstByte, err := reader.Peek(1)
	if err != nil {
		h.HandleError(fmt.Errorf("failed to peek connection data: %w", err), "HandleConnection")
		return
	}

	// 创建连接对等体
	peer := ConnPeer{
		server: h,
		conn:   conn,
		writer: writer,
		reader: reader,
	}

	// 根据第一个字节确定协议处理器
	var process ProtocolProcessor
	switch firstByte[0] {
	case MethodGet, MethodPost, MethodDelete, MethodOptions, MethodHead, MethodConnect:
		// HTTP协议：可能需要进一步检测完整的请求行
		process = &ProxyHttp{ConnPeer: peer}
	case SocksFive:
		// SOCKS5协议：第一个字节是版本号
		process = &ProxySocks5{ConnPeer: peer}
	case TLSHandshake, TLSAlert, TLSAppData:
		// TLS/SSL协议
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected TLS/SSL protocol (byte: 0x%02x)", firstByte[0])
		}
		process = &ProxyTcp{ConnPeer: peer} // 暂时使用TCP处理
	case SSHVersion:
		// SSH协议，需要进一步验证 "SSH-2.0" 或 "SSH-1.99"
		process = h.detectSSHProtocol(reader, peer)
	case FTPResponse:
		// FTP响应或其他以数字开头的协议，需要进一步检测
		process = h.detectNumericProtocol(reader, peer)
	case MQTTConnect:
		// MQTT协议
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected potential MQTT protocol")
		}
		process = &ProxyTcp{ConnPeer: peer}
	case RDPRequest:
		// RDP协议
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected potential RDP protocol")
		}
		process = &ProxyTcp{ConnPeer: peer}
	default:
		// 未知协议，进行高级检测或使用默认TCP处理器
		if h.needsAdvancedDetection(firstByte[0]) {
			process = h.detectAdvancedProtocol(reader, peer)
		} else {
			process = &ProxyTcp{ConnPeer: peer}
		}
	}

	if h.config.IsLoggingEnabled() {
		h.logInfo("Detected protocol: %s for connection %s", process.GetProtocolName(), remoteAddr)
	}

	// 处理连接
	err = process.Process()
	if err != nil {
		h.HandleError(fmt.Errorf("protocol processing failed: %w", err), "HandleConnection")
		return
	}

	if h.config.IsLoggingEnabled() {
		h.logInfo("Connection %s processed successfully", remoteAddr)
	}
}

// detectSSHProtocol 检测SSH协议
func (h *DefaultPacketHandler) detectSSHProtocol(reader *bufio.Reader, peer ConnPeer) ProtocolProcessor {
	// SSH协议的识别字符串：SSH-2.0-xxx 或 SSH-1.99-xxx
	sshHeader, err := reader.Peek(8) // 读取 "SSH-2.0-" 或 "SSH-1.99"
	if err != nil {
		return &ProxyTcp{ConnPeer: peer}
	}

	if len(sshHeader) >= 7 && string(sshHeader[:7]) == "SSH-2.0" {
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected SSH-2.0 protocol")
		}
		return &ProxyTcp{ConnPeer: peer}
	} else if len(sshHeader) >= 8 && string(sshHeader[:8]) == "SSH-1.99" {
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected SSH-1.99 protocol")
		}
		return &ProxyTcp{ConnPeer: peer}
	}

	// 不是SSH协议，返回TCP处理器
	return &ProxyTcp{ConnPeer: peer}
}

// detectNumericProtocol 检测以数字开头的协议（如FTP、SMTP等）
func (h *DefaultPacketHandler) detectNumericProtocol(reader *bufio.Reader, peer ConnPeer) ProtocolProcessor {
	// 读取更多字节来判断具体协议
	header, err := reader.Peek(12)
	if err != nil {
		return &ProxyTcp{ConnPeer: peer}
	}

	headerStr := string(header)

	// FTP协议检测
	if len(header) >= 3 {
		if headerStr[:3] == "220" { // FTP服务就绪
			if h.config.IsLoggingEnabled() {
				h.logInfo("Detected FTP protocol (220 response)")
			}
			return &ProxyTcp{ConnPeer: peer}
		}
		if headerStr[:3] == "230" { // FTP用户登录成功
			if h.config.IsLoggingEnabled() {
				h.logInfo("Detected FTP protocol (230 response)")
			}
			return &ProxyTcp{ConnPeer: peer}
		}
	}

	// SMTP协议检测
	if len(header) >= 3 && headerStr[:3] == "220" {
		// 进一步检查是否包含SMTP特征
		if len(header) >= 8 && (headerStr[4:8] == "SMTP" || headerStr[4:8] == "smtp") {
			if h.config.IsLoggingEnabled() {
				h.logInfo("Detected SMTP protocol")
			}
			return &ProxyTcp{ConnPeer: peer}
		}
	}

	// POP3协议检测
	if len(header) >= 4 && headerStr[:4] == "+OK " {
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected POP3 protocol")
		}
		return &ProxyTcp{ConnPeer: peer}
	}

	// 默认返回TCP处理器
	return &ProxyTcp{ConnPeer: peer}
}

// needsAdvancedDetection 判断是否需要进行高级协议检测
func (h *DefaultPacketHandler) needsAdvancedDetection(firstByte byte) bool {
	// 对于一些特殊字节，可能需要读取更多数据来确定协议
	switch firstByte {
	case 0x16: // TLS handshake（已在主检测中处理）
		return false
	case 0x80, 0x81, 0x82: // 一些二进制协议
		return true
	case 0x00: // 可能是DNS查询或其他协议
		return true
	case '+': // POP3的 +OK（如果第一个字节是+的话）
		return true
	case '-': // 某些协议的错误响应
		return true
	default:
		// 检查是否是可打印ASCII字符但不是已知协议
		if firstByte >= 32 && firstByte <= 126 &&
			firstByte != 'G' && firstByte != 'P' && firstByte != 'D' &&
			firstByte != 'O' && firstByte != 'H' && firstByte != 'C' &&
			firstByte != 'S' && firstByte != '2' {
			return true
		}
		return false
	}
}

// detectAdvancedProtocol 执行高级协议检测，需要读取更多字节
func (h *DefaultPacketHandler) detectAdvancedProtocol(reader *bufio.Reader, peer ConnPeer) ProtocolProcessor {
	// 尝试读取更多字节进行协议检测
	moreBytes, err := reader.Peek(32) // 读取最多32字节进行检测
	if err != nil {
		h.logError("Advanced protocol detection failed: %v", err)
		return &ProxyTcp{ConnPeer: peer}
	}

	if len(moreBytes) == 0 {
		return &ProxyTcp{ConnPeer: peer}
	}

	dataStr := string(moreBytes)

	// DNS协议检测（通常第一个字节是0x00）
	if moreBytes[0] == 0x00 && len(moreBytes) >= 12 {
		// DNS查询包结构检查
		if h.isDNSPacket(moreBytes) {
			if h.config.IsLoggingEnabled() {
				h.logInfo("Detected DNS protocol")
			}
			return &ProxyTcp{ConnPeer: peer}
		}
	}

	// IMAP 协议检测
	if h.isIMAPProtocol(dataStr) {
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected IMAP protocol")
		}
		return &ProxyTcp{ConnPeer: peer}
	}

	// Telnet协议检测
	if h.isTelnetProtocol(moreBytes) {
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected Telnet protocol")
		}
		return &ProxyTcp{ConnPeer: peer}
	}

	// 二进制协议检测（如数据库协议）
	if h.isBinaryProtocol(moreBytes) {
		if h.config.IsLoggingEnabled() {
			h.logInfo("Detected binary protocol")
		}
		return &ProxyTcp{ConnPeer: peer}
	}

	// 默认 TCP 转发
	return &ProxyTcp{ConnPeer: peer}
}

// isDNSPacket 检测是否为 DNS 数据包
func (h *DefaultPacketHandler) isDNSPacket(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	// DNS 头部结构简单检查
	// 事务ID(2) + 标志(2) + 问题数(2) + 答案数(2) + 权威数(2) + 附加数(2)
	flags := (uint16(data[2]) << 8) | uint16(data[3])
	// 检查QR位（查询/响应）和操作码
	return (flags & 0x7800) == 0x0000 // 标准查询
}

// isIMAPProtocol 检测 IMAP 协议
func (h *DefaultPacketHandler) isIMAPProtocol(data string) bool {
	// IMAP命令通常以标签开头，如: A001 LOGIN, * OK IMAP4rev1
	if len(data) < 4 {
		return false
	}
	return data[:4] == "* OK" ||
		(len(data) > 8 && (contains(data, " LOGIN") ||
			contains(data, " CAPABILITY") ||
			contains(data, " SELECT")))
}

// isTelnetProtocol 检测 Telnet 协议（通常包含 IAC 控制字符）
func (h *DefaultPacketHandler) isTelnetProtocol(data []byte) bool {
	if len(data) < 3 {
		return false
	}
	// 检查是否包含 Telnet 的 IAC 字符 (0xFF)
	for i := 0; i < len(data)-2; i++ {
		if data[i] == 0xFF && data[i+1] >= 0xFB && data[i+1] <= 0xFE {
			return true
		}
	}
	return false
}

// isBinaryProtocol 检测二进制协议
func (h *DefaultPacketHandler) isBinaryProtocol(data []byte) bool {
	if len(data) < 4 {
		return false
	}

	// 检查非可打印字符的比例
	nonPrintable := 0
	for _, b := range data[:min(16, len(data))] {
		if b < 32 && b != 9 && b != 10 && b != 13 { // 除了tab、换行、回车
			nonPrintable++
		}
	}

	// 如果超过一半是非可打印字符，认为是二进制协议
	return nonPrintable > len(data[:min(16, len(data))])/2
}

// 辅助函数
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		s[len(s)-len(substr):] == substr ||
		len(s) > len(substr) && s[:len(substr)] == substr ||
		len(s) > len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// HandleError 处理错误
func (h *DefaultPacketHandler) HandleError(err error, context string) {
	h.logError("Error in %s: %v", context, err)
}

// OnConnectionStart 连接开始时的回调
func (h *DefaultPacketHandler) OnConnectionStart(conn net.Conn) error {
	if h.config.IsLoggingEnabled() {
		h.logInfo("Connection started: %s -> %s",
			conn.RemoteAddr().String(),
			conn.LocalAddr().String())
	}

	// TODO: 连接计数 速率限制检查 黑名单检查

	return nil
}

// OnConnectionEnd 连接结束时的回调
func (h *DefaultPacketHandler) OnConnectionEnd(conn net.Conn, duration time.Duration) {
	if h.config.IsLoggingEnabled() {
		h.logInfo("Connection ended: %s (duration: %s)",
			conn.RemoteAddr().String(),
			duration.String())
	}

	// TODO: 连接统计 清理资源 记录会话信息
}

// formatDataPreview 格式化数据预览（只显示前64字节）
func (h *DefaultPacketHandler) formatDataPreview(data []byte) string {
	maxLen := 64
	if len(data) > maxLen {
		return fmt.Sprintf("%q... (%d bytes total)", data[:maxLen], len(data))
	}
	return fmt.Sprintf("%q", data)
}

// logInfo 记录信息日志
func (h *DefaultPacketHandler) logInfo(format string, args ...interface{}) {
	if h.logger != nil {
		h.logger.Info(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// logError 记录错误日志
func (h *DefaultPacketHandler) logError(format string, args ...interface{}) {
	if h.logger != nil {
		h.logger.Error(format, args...)
	} else {
		log.Printf("ERROR: "+format, args...)
	}
}
