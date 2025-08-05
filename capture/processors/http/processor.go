// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/types"
	"golang.org/x/net/websocket"
)

var selfCA ca.CA
var sharedHttpClient *http.Client

func init() {
	var err error
	selfCA, err = ca.NewSelfSignedCA()
	if err != nil {
		panic(err)
	}

	// 初始化共享的HTTP客户端，配置连接池
	sharedHttpClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // 忽略HTTPS证书
			},
			// 连接池配置
			MaxIdleConns:        1000,             // 最大空闲连接数
			MaxIdleConnsPerHost: 100,              // 每个主机的最大空闲连接数
			MaxConnsPerHost:     500,              // 每个主机的最大连接数
			IdleConnTimeout:     90 * time.Second, // 空闲连接超时时间
			DisableKeepAlives:   false,            // 启用keep-alive
			// TCP连接配置
			ResponseHeaderTimeout: 30 * time.Second, // 响应头超时
			ExpectContinueTimeout: 1 * time.Second,  // 100-continue超时
		},
		Timeout: 10 * time.Minute,
	}
}

// readerConn 包装连接，确保TLS可以从bufio.Reader读取数据
type readerConn struct {
	net.Conn
	reader *bufio.Reader
}

// Read 从bufio.Reader读取数据
func (rc *readerConn) Read(b []byte) (n int, err error) {
	return rc.reader.Read(b)
}

// Processor HTTP协议处理器
type Processor struct {
	conn    types.Connection
	request *http.Request
	isHttps bool
}

// New 创建新的HTTP处理器
func New(conn types.Connection) types.ProtocolProcessor {
	return &Processor{
		conn: conn,
	}
}

// GetProtocolName 返回协议名称
func (p *Processor) GetProtocolName() string {
	return "HTTP"
}

// Process 处理HTTP协议
func (p *Processor) Process() error {
	server := p.conn.GetServer()
	reader := p.conn.GetReader()
	writer := p.conn.GetWriter()

	// 执行具体的HTTP协议处理逻辑
	return p.handleHttpProtocol(server, reader, writer)
}

// handleHttpProtocol 处理HTTP协议的具体逻辑
func (p *Processor) handleHttpProtocol(server types.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	// HTTP协议处理逻辑
	server.LogDebug("处理HTTP协议...")

	request, err := http.ReadRequest(reader)
	if err != nil {
		server.LogError("读取HTTP请求失败: %v", err)
		return err
	}

	p.request = request

	server.LogDebug("请求的域名是：" + request.Host)

	isWebSocket := p.request.Header.Get("Upgrade") == "websocket" && p.request.Header.Get("Connection") == "Upgrade"

	// 如果是CONNECT请求，则处理TLS握手
	if p.request.Method == http.MethodConnect {
		fmt.Println(p.request.Header)
		p.isHttps = true
		server.LogDebug("处理TLS握手请求")
		p.handleTlsHandshake(server, writer)
	}

	if isWebSocket {
		server.LogDebug("处理WebSocket请求")
		return p.handleWebSocket(server)
	}

	return p.handleRequest(server)
}

func (p *Processor) handleWebSocket(server types.Server) error {
	server.LogDebug("开始处理WebSocket连接")

	// 构建目标WebSocket URL
	targetURL := p.buildWebSocketURL()
	server.LogDebug("目标WebSocket URL: %s", targetURL)

	// 创建WebSocket配置
	config, err := websocket.NewConfig(targetURL, p.getOrigin())
	if err != nil {
		server.LogError("创建WebSocket配置失败: %v", err)
		return err
	}

	// 复制原始请求的头部信息
	p.copyWebSocketHeaders(config)

	// 建立与目标服务器的WebSocket连接
	targetConn, err := websocket.DialConfig(config)
	if err != nil {
		server.LogError("连接目标WebSocket服务器失败: %v", err)
		return p.sendWebSocketError()
	}
	defer targetConn.Close()

	server.LogInfo("WebSocket连接建立成功，开始代理数据")

	// 创建WebSocket处理器，让它处理客户端连接
	wsServer := &websocket.Server{
		Handler: func(clientWs *websocket.Conn) {
			defer clientWs.Close()
			server.LogDebug("客户端WebSocket连接已建立")

			// 开始双向数据转发
			p.proxyWebSocketData(server, clientWs, targetConn)
		},
	}

	// 创建一个假的ResponseWriter来处理WebSocket升级
	responseWriter := &fakeResponseWriter{conn: p.conn.GetConn()}

	// 处理WebSocket握手和升级
	wsServer.ServeHTTP(responseWriter, p.request)

	return nil
}

// buildWebSocketURL 构建目标WebSocket URL
func (p *Processor) buildWebSocketURL() string {
	scheme := "ws"
	if p.isHttps {
		scheme = "wss"
	}

	return fmt.Sprintf("%s://%s%s", scheme, p.request.Host, p.request.URL.Path)
}

// getOrigin 获取Origin头
func (p *Processor) getOrigin() string {
	origin := p.request.Header.Get("Origin")
	if origin == "" {
		// 如果没有Origin头，使用Host构建
		scheme := "http"
		if p.isHttps {
			scheme = "https"
		}
		origin = fmt.Sprintf("%s://%s", scheme, p.request.Host)
	}
	return origin
}

// copyWebSocketHeaders 复制WebSocket相关的头部信息
func (p *Processor) copyWebSocketHeaders(config *websocket.Config) {
	// 复制重要的WebSocket头部
	if subprotocol := p.request.Header.Get("Sec-WebSocket-Protocol"); subprotocol != "" {
		config.Protocol = []string{subprotocol}
	}

	// 复制其他相关头部
	for key, values := range p.request.Header {
		switch key {
		case "Sec-WebSocket-Extensions", "Sec-WebSocket-Key", "Sec-WebSocket-Version":
			// 这些头部由websocket包自动处理
			continue
		case "Host", "Connection", "Upgrade":
			// 这些头部不需要转发
			continue
		default:
			// 转发其他头部
			for _, value := range values {
				config.Header.Add(key, value)
			}
		}
	}
}

// sendWebSocketError 发送WebSocket错误响应
func (p *Processor) sendWebSocketError() error {
	const errorResp = "HTTP/1.1 502 Bad Gateway\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 28\r\n" +
		"\r\n" +
		"WebSocket connection failed"

	writer := p.conn.GetWriter()
	if _, err := writer.WriteString(errorResp); err != nil {
		return err
	}
	return writer.Flush()
}

// proxyWebSocketData 代理WebSocket数据
func (p *Processor) proxyWebSocketData(server types.Server, clientWs, targetConn *websocket.Conn) {
	defer targetConn.Close()

	var wg sync.WaitGroup

	// 客户端到服务器的消息转发
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := p.forwardWebSocketFrames(clientWs, targetConn, "client->server", server); err != nil {
			if err != io.EOF {
				server.LogError("客户端到服务器数据转发失败: %v", err)
			}
		}
	}()

	// 服务器到客户端的消息转发
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := p.forwardWebSocketFrames(targetConn, clientWs, "server->client", server); err != nil {
			if err != io.EOF {
				server.LogError("服务器到客户端数据转发失败: %v", err)
			}
		}
	}()

	// 等待两个方向都完成
	wg.Wait()
	server.LogInfo("WebSocket连接正常关闭")
}

// fakeResponseWriter 实现http.ResponseWriter接口用于WebSocket升级
type fakeResponseWriter struct {
	conn net.Conn
}

func (f *fakeResponseWriter) Header() http.Header {
	return make(http.Header)
}

func (f *fakeResponseWriter) Write(data []byte) (int, error) {
	return f.conn.Write(data)
}

func (f *fakeResponseWriter) WriteHeader(statusCode int) {
	// 什么都不做，因为WebSocket升级不需要状态码
}

// Hijack 实现http.Hijacker接口，允许WebSocket接管底层连接
func (f *fakeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// 创建一个 ReadWriter 来包装连接
	rw := bufio.NewReadWriter(bufio.NewReader(f.conn), bufio.NewWriter(f.conn))
	return f.conn, rw, nil
}

// forwardWebSocketFrames 直接转发WebSocket帧，保持消息类型
func (p *Processor) forwardWebSocketFrames(src, dst *websocket.Conn, direction string, server types.Server) error {
	buffer := make([]byte, 32*1024) // 32KB缓冲区

	for {
		// 尝试设置读取超时（如果支持的话）
		if conn, ok := any(src).(interface{ SetReadDeadline(time.Time) error }); ok {
			if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
				return err
			}
		}

		// 读取原始WebSocket数据
		n, err := src.Read(buffer)
		if err != nil {
			if err == io.EOF {
				server.LogDebug("WebSocket连接 %s 正常关闭", direction)
				return nil
			}
			// 检查是否是连接关闭错误
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				server.LogDebug("WebSocket连接 %s 超时", direction)
				return err
			}
			return err
		}

		if n > 0 {
			// 尝试设置写入超时（如果支持的话）
			if conn, ok := any(dst).(interface{ SetWriteDeadline(time.Time) error }); ok {
				if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
					return err
				}
			}

			// 直接转发原始数据，保持WebSocket帧的完整性
			_, err := dst.Write(buffer[:n])
			if err != nil {
				return err
			}

			server.LogDebug("WebSocket %s 转发了 %d 字节原始数据", direction, n)
		}
	}
}

// TLS握手
func (p *Processor) handleTlsHandshake(server types.Server, writer *bufio.Writer) error {

	// 必须先发送CONNECT响应，告诉客户端连接已建立
	const response = "HTTP/1.1 200 Connection Established\r\n\r\n"
	if _, err := writer.WriteString(response); err != nil {
		server.LogError("发送CONNECT响应失败: %v", err)
		return err
	}
	if err := writer.Flush(); err != nil {
		server.LogError("刷新CONNECT响应失败: %v", err)
		return err
	}

	reader := p.conn.GetReader()
	firstByte, err := reader.Peek(1)
	if err != nil {
		server.LogError("读取第一个字节失败: %v", err)
		return err
	}

	// 判断客户端意图
	switch firstByte[0] {
	case 0x16: // TLS握手记录类型
		server.LogDebug("检测到TLS握手：0x%02x", firstByte[0])
	case 0x47: // 'G' - 可能是GET请求
		server.LogDebug("检测到HTTP请求：0x%02x", firstByte[0])
		p.isHttps = false

		p.request, err = http.ReadRequest(reader)
		if err != nil {
			server.LogError("读取HTTP请求失败: %v", err)
			return err
		}
		return p.handleWebSocket(server)
	case 0x50: // 'P' - 可能是POST请求
		server.LogDebug("检测到HTTP请求：0x%02x", firstByte[0])
	default:
		server.LogDebug("未知协议，第一个字节：0x%02x", firstByte[0])
	}

	// 创建包装连接，让TLS能够从bufio.Reader读取数据
	conn := &readerConn{
		Conn:   p.conn.GetConn(),
		reader: reader,
	}

	// 伪造tls握手
	cert, err := selfCA.IssueCert(p.request.Host)
	if err != nil {
		server.LogError("生成证书失败: %v", err)
		return err
	}

	// 设置TLS握手超时
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		server.LogError("设置连接超时失败: %v", err)
		return err
	}

	connSsl := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{*cert},
	})

	if err := connSsl.Handshake(); err != nil {
		server.LogError("TLS握手失败: %v", err)
		return err
	}

	_ = connSsl.SetDeadline(time.Now().Add(time.Minute * 5))
	p.conn.SetConn(connSsl)

	// 清空请求，避免重复处理
	p.request = nil

	return nil
}

// 转发请求
func (p *Processor) handleRequest(server types.Server) error {

	var request *http.Request
	if p.request == nil {
		var err error
		request, err = http.ReadRequest(p.conn.GetReader())
		if err != nil {
			server.LogError("读取HTTP请求失败: %v", err)
			return err
		}
	} else {
		request = p.request
	}

	// 构建完整的URL
	if request.URL.Scheme == "" {
		if p.isHttps {
			request.URL.Scheme = "https"
		} else {
			request.URL.Scheme = "http"
		}
	}
	if request.URL.Host == "" {
		request.URL.Host = request.Host
	}

	// 清空RequestURI，避免客户端请求错误
	request.RequestURI = ""

	// 发起请求 (使用共享连接池)
	resp, err := sharedHttpClient.Do(request)
	if err != nil {
		server.LogError("请求失败: %v", err)
		// 返回502错误
		errorResp := "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 15\r\n\r\n502 Bad Gateway"
		writer := p.conn.GetWriter()
		_, _ = writer.WriteString(errorResp)
		return writer.Flush()
	}
	defer resp.Body.Close()

	// 获取原始连接，直接写入响应
	err = resp.Write(p.conn.GetConn())
	if err != nil {
		server.LogError("写入响应失败: %v", err)
		return err
	}

	return nil
}
