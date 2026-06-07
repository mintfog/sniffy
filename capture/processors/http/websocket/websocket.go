// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/plugins"
	"golang.org/x/net/websocket"
)

// Processor WebSocket协议处理器
type Processor struct {
	conn        types.Connection
	request     *http.Request
	isHttps     bool
	interceptor *MessageInterceptor
	recorder    *wsRecorder // 会话记录器(wsSink 注入时启用)
	targetURL   string      // 目标 WebSocket URL(供消息 URL 门控)
}

// New 创建新的WebSocket处理器
func New(conn types.Connection, request *http.Request, isHttps bool) *Processor {
	return &Processor{
		conn:    conn,
		request: request,
		isHttps: isHttps,
	}
}

// SetHookExecutor 设置插件钩子执行器
func (p *Processor) SetHookExecutor(hookExecutor *plugins.HookExecutor) {
	if hookExecutor != nil {
		server := p.conn.GetServer()
		logger := &LoggerAdapter{server: server}
		p.interceptor = NewMessageInterceptor(hookExecutor, logger, p.request)
	}
}

// LoggerAdapter 适配器，将types.Server转换为types.Logger
type LoggerAdapter struct {
	server types.Server
}

func (la *LoggerAdapter) Info(msg string, args ...interface{}) {
	la.server.LogInfo(msg, args...)
}

func (la *LoggerAdapter) Error(msg string, args ...interface{}) {
	la.server.LogError(msg, args...)
}

func (la *LoggerAdapter) Debug(msg string, args ...interface{}) {
	la.server.LogDebug(msg, args...)
}

func (la *LoggerAdapter) Warn(msg string, args ...interface{}) {
	la.server.LogInfo("[WARN] "+msg, args...)
}

// Process 处理WebSocket连接
func (p *Processor) Process(server types.Server) error {
	server.LogDebug("开始处理WebSocket连接")

	// 构建目标WebSocket URL
	targetURL := p.buildWebSocketURL()
	p.targetURL = targetURL
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

	// 登记一条 WebSocket 会话(供 UI 实时展示),并异步补进程信息。
	p.recorder = newWSRecorder(targetURL)
	p.resolveProcessAsync()
	defer p.recorder.close()

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

// interceptViaPipeline 把一帧消息送入新插件管道并记录会话。
// 返回(可能被插件改写的)数据,以及是否应丢弃该帧(插件 abort)。
func (p *Processor) interceptViaPipeline(data []byte, direction string, server types.Server) ([]byte, bool) {
	msgType := flow.WSBinary
	if utf8.Valid(data) {
		msgType = flow.WSText
	}
	m := &flow.WSMessage{
		ID:        flow.NewID(),
		FlowID:    p.recorder.id(),
		URL:       p.targetURL,
		Direction: direction,
		Type:      msgType,
		Data:      append([]byte(nil), data...),
		Timestamp: time.Now(),
	}
	d := activePipeline.OnWebSocketMessage(context.Background(), m)
	if d.Kind == flow.Abort {
		server.LogInfo("WebSocket消息被插件 abort: %s", d.Reason)
		return nil, true
	}
	p.recorder.record(direction, msgType, m.Data)
	return m.Data, false
}

// resolveProcessAsync 异步解析发起进程并挂到 WebSocket 会话(best-effort)。
func (p *Processor) resolveProcessAsync() {
	if processResolver == nil || p.recorder == nil {
		return
	}
	conn := p.conn.GetConn()
	if conn == nil {
		return
	}
	clientAddr, proxyAddr := conn.RemoteAddr(), conn.LocalAddr()
	go func() {
		if pi := processResolver.Resolve(clientAddr, proxyAddr); pi != nil {
			p.recorder.setProcess(pi)
		}
	}()
}

// forwardWebSocketFrames 转发WebSocket帧，支持插件拦截
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
			messageData := buffer[:n]

			// 经 pipeline.OnWebSocketMessage 送入插件管道并记录会话消息。
			if activePipeline != nil {
				if data, drop := p.interceptViaPipeline(messageData, direction, server); drop {
					continue // 被插件 abort,丢弃该帧
				} else {
					messageData = data
				}
			} else if p.interceptor != nil {
				// 兼容路径:未注入 pipeline 时沿用 MessageInterceptor(独立测试场景)。
				var msgDirection plugins.WebSocketDirection
				if direction == flow.WSClientToServer {
					msgDirection = plugins.ClientToServer
				} else {
					msgDirection = plugins.ServerToClient
				}
				interceptedData, err := p.interceptor.InterceptMessage(messageData, plugins.BinaryMessage, msgDirection, p.conn)
				if err != nil {
					if _, ok := err.(*InterceptError); ok {
						server.LogInfo("WebSocket消息被插件拦截: %v", err)
						continue
					}
					server.LogError("WebSocket消息拦截器错误: %v", err)
				} else if interceptedData != nil {
					messageData = interceptedData
				}
			}

			// 尝试设置写入超时（如果支持的话）
			if conn, ok := any(dst).(interface{ SetWriteDeadline(time.Time) error }); ok {
				if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
					return err
				}
			}

			// 转发处理后的数据
			_, err := dst.Write(messageData)
			if err != nil {
				return err
			}

			server.LogDebug("WebSocket %s 转发了 %d 字节数据", direction, len(messageData))
		}
	}
}

// IsWebSocketRequest 检查请求是否为WebSocket升级请求
func IsWebSocketRequest(request *http.Request) bool {
	return request.Header.Get("Upgrade") == "websocket" && request.Header.Get("Connection") == "Upgrade"
}
