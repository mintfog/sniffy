// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"golang.org/x/net/websocket"
)

// Processor WebSocket协议处理器
type Processor struct {
	conn    types.Connection
	request *http.Request
	isHttps bool
}

// New 创建新的WebSocket处理器
func New(conn types.Connection, request *http.Request, isHttps bool) *Processor {
	return &Processor{
		conn:    conn,
		request: request,
		isHttps: isHttps,
	}
}

// Process 处理WebSocket连接
func (p *Processor) Process(server types.Server) error {
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

// IsWebSocketRequest 检查请求是否为WebSocket升级请求
func IsWebSocketRequest(request *http.Request) bool {
	return request.Header.Get("Upgrade") == "websocket" && request.Header.Get("Connection") == "Upgrade"
}
