// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/plugins"
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

// Process 处理 WebSocket 连接:以「保真」方式转发握手(客户端原始头序列/大小写/Key 原样
// 透传给上游,上游的 101 原样转回客户端),随后做帧级双向代理(保留插件拦截与会话记录)。
func (p *Processor) Process(server types.Server) error {
	server.LogDebug("开始处理WebSocket连接")
	p.targetURL = p.buildWebSocketURL()
	server.LogDebug("目标WebSocket URL: %s", p.targetURL)

	upstream, upstreamBr, respBytes, status, err := p.dialUpstreamFaithful()
	if err != nil {
		server.LogError("连接目标WebSocket服务器失败: %v", err)
		return p.sendWebSocketError()
	}
	defer upstream.Close()

	clientConn := p.conn.GetConn()
	// 把上游握手响应原样写回客户端;101 的 Sec-WebSocket-Accept 由客户端自己的 Key 推得,
	// 故与客户端预期自洽。
	if _, err := clientConn.Write(respBytes); err != nil {
		server.LogError("写回 WebSocket 握手响应失败: %v", err)
		return err
	}
	if status != http.StatusSwitchingProtocols {
		// 上游拒绝升级:把其余响应(body)透传给客户端后结束。
		server.LogInfo("WebSocket 上游未升级 (status=%d),已转回客户端", status)
		_, _ = io.Copy(clientConn, upstreamBr)
		return nil
	}

	server.LogInfo("WebSocket连接建立成功，开始代理数据: %s", p.targetURL)
	// 登记一条 WebSocket 会话(供 UI 实时展示),并异步补进程信息。
	p.recorder = newWSRecorder(p.targetURL)
	p.resolveProcessAsync()
	defer p.recorder.close()

	p.proxyFrames(server, clientConn, p.conn.GetReader(), upstream, upstreamBr)
	return nil
}

// proxyFrames 帧级双向代理。任一方向结束(对端关闭/出错/收到 Close 帧)即关闭两端,
// 唤醒另一方向阻塞中的读,既不泄漏 goroutine,也不因空闲读超时误杀长连接。
func (p *Processor) proxyFrames(server types.Server, clientConn net.Conn, clientBr *bufio.Reader, upstream net.Conn, upstreamBr *bufio.Reader) {
	// 清除连接上残留的绝对超时(wss 在 TLS 握手后曾被设超时;WebSocket 长连接需置零)。
	clearRawDeadline(clientConn)
	clearRawDeadline(upstream)

	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = clientConn.Close()
			_ = upstream.Close()
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	// 客户端 → 上游:客户端帧带掩码;作为客户端发往上游须(重新)加掩。
	go func() {
		defer wg.Done()
		defer closeBoth()
		p.pumpFrames(server, clientBr, upstream, true, flow.WSClientToServer)
	}()
	// 上游 → 客户端:上游(服务端)帧无掩码;发往客户端不加掩。
	go func() {
		defer wg.Done()
		defer closeBoth()
		p.pumpFrames(server, upstreamBr, clientConn, false, flow.WSServerToClient)
	}()
	wg.Wait()
	server.LogInfo("WebSocket连接正常关闭")
}

// pumpFrames 从 src 逐帧读出:数据帧经插件拦截/记录(可被改写或丢弃),控制帧原样转发;
// 再按 maskOut 角色写到 dst。收到 Close 帧时转发后结束本方向。
func (p *Processor) pumpFrames(server types.Server, src *bufio.Reader, dst io.Writer, maskOut bool, direction string) {
	for {
		fr, err := readFrame(src)
		if err != nil {
			if err != io.EOF {
				server.LogDebug("WebSocket %s 读帧结束: %v", direction, err)
			}
			return
		}
		if fr.isData() {
			data, drop := p.handleFrameData(fr.payload, direction, server)
			if drop {
				continue // 插件 abort:丢弃该帧
			}
			fr.payload = data
		}
		if err := writeFrame(dst, fr, maskOut); err != nil {
			server.LogDebug("WebSocket %s 写帧失败: %v", direction, err)
			return
		}
		if fr.opcode == opClose {
			return // 关闭帧已转发,本方向结束
		}
	}
}

// handleFrameData 把数据帧 payload 送入插件管道(或兼容拦截器)并记录会话,
// 返回(可能被改写的)payload 与是否应丢弃该帧。
func (p *Processor) handleFrameData(data []byte, direction string, server types.Server) ([]byte, bool) {
	msgType := flow.WSBinary
	if utf8.Valid(data) {
		msgType = flow.WSText
	}

	if activePipeline != nil {
		m := &flow.WSMessage{
			ID:        flow.NewID(),
			FlowID:    p.recorder.id(),
			URL:       p.targetURL,
			Direction: direction,
			Type:      msgType,
			Data:      append([]byte(nil), data...),
			Timestamp: time.Now(),
		}
		if d := activePipeline.OnWebSocketMessage(context.Background(), m); d.Kind == flow.Abort {
			server.LogInfo("WebSocket消息被插件 abort: %s", d.Reason)
			return nil, true
		}
		p.recorder.record(direction, msgType, m.Data)
		return m.Data, false
	}

	if p.interceptor != nil {
		// 兼容路径:未注入 pipeline 时沿用 MessageInterceptor(独立测试场景)。
		var dir plugins.WebSocketDirection
		if direction == flow.WSClientToServer {
			dir = plugins.ClientToServer
		} else {
			dir = plugins.ServerToClient
		}
		if out, err := p.interceptor.InterceptMessage(data, plugins.BinaryMessage, dir, p.conn); err != nil {
			if _, ok := err.(*InterceptError); ok {
				return nil, true // 被拦截器丢弃
			}
			server.LogError("WebSocket消息拦截器错误: %v", err)
		} else if out != nil {
			data = out
		}
	}
	if p.recorder != nil {
		p.recorder.record(direction, msgType, data)
	}
	return data, false
}

// clearRawDeadline 清除连接上已设置的读写超时(置零=永不超时)。
func clearRawDeadline(c net.Conn) {
	if c != nil {
		_ = c.SetDeadline(time.Time{})
	}
}

// buildWebSocketURL 构建目标WebSocket URL。复制原始请求 URL 仅覆盖 scheme/host。
func (p *Processor) buildWebSocketURL() string {
	scheme := "ws"
	if p.isHttps {
		scheme = "wss"
	}
	target := *p.request.URL // 复制,避免改动原始请求
	target.Scheme = scheme
	target.Host = p.request.Host
	target.Fragment = ""
	return target.String()
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

// IsWebSocketRequest 检查请求是否为WebSocket升级请求
func IsWebSocketRequest(request *http.Request) bool {
	return request.Header.Get("Upgrade") == "websocket" && request.Header.Get("Connection") == "Upgrade"
}
