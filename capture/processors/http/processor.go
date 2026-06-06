// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/processors/http/websocket"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/plugins"
)

var selfCA ca.CA
var sharedHttpClient *http.Client

// activePipeline 是新的插件管道(基于 flow.Flow + flow.Decision)。
// 为 nil 时处理器退化为简单转发(兼容独立测试)。
var activePipeline *pipeline.Pipeline

// flowSink 接收抓到的 flow,用于写入 service(会话/统计)。
var flowSink FlowSink

// FlowSink 由 service 实现,处理器经此把 flow 写入存储(消费者定义接口,避免反向依赖)。
type FlowSink interface {
	RecordFlowStarted(f *flow.Flow)
	RecordFlowCompleted(f *flow.Flow)
}

// SetPipeline 注入插件管道。
func SetPipeline(p *pipeline.Pipeline) { activePipeline = p }

// SetFlowSink 注入 flow 接收器。
func SetFlowSink(s FlowSink) { flowSink = s }

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
			MaxIdleConns:        MaxIdleConns,
			MaxIdleConnsPerHost: MaxIdleConnsPerHost,
			MaxConnsPerHost:     MaxConnsPerHost,
			IdleConnTimeout:     IdleConnTimeout,
			DisableKeepAlives:   false, // 启用keep-alive
			// TCP连接配置
			ResponseHeaderTimeout: ResponseHeaderTimeout,
			ExpectContinueTimeout: ExpectContinueTimeout,
		},
		Timeout: ClientTimeout,
	}
}

// SetCA 注入由引擎层(internal/core)持有的 CA,覆盖包级默认值。
// 传入 nil 时保留现有值,以兼容不经引擎的独立测试。
func SetCA(c ca.CA) {
	if c != nil {
		selfCA = c
	}
}

// SetUpstreamClient 注入由引擎层持有的上游 HTTP 客户端,覆盖包级默认值。
// 传入 nil 时保留现有值。
func SetUpstreamClient(c *http.Client) {
	if c != nil {
		sharedHttpClient = c
	}
}

// Processor HTTP协议处理器
type Processor struct {
	conn        types.Connection
	request     *http.Request
	isHttps     bool
	interceptor *RequestInterceptor
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

// SetHookExecutor 设置插件钩子执行器
func (p *Processor) SetHookExecutor(hookExecutor *plugins.HookExecutor) {
	if hookExecutor != nil {
		server := p.conn.GetServer()
		logger := &LoggerAdapter{server: server}
		p.interceptor = NewRequestInterceptor(hookExecutor, logger)
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

	// 如果是CONNECT请求，交给专门的处理方法
	if p.request.Method == http.MethodConnect {
		server.LogDebug("处理CONNECT请求")
		return p.handleConnect(server, reader, writer)
	}

	// 检查是否为WebSocket请求
	if websocket.IsWebSocketRequest(p.request) {
		server.LogDebug("处理WebSocket请求")
		return p.handleWebSocket(server)
	}

	// 处理普通HTTP请求
	return p.handleRequest(server)
}

// handleConnect 专门处理CONNECT请求
func (p *Processor) handleConnect(server types.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	server.LogDebug("处理CONNECT请求，目标地址：%s", p.request.Host)

	// 发送CONNECT响应，告诉客户端连接已建立
	if _, err := writer.WriteString(ConnectEstablishedResponse); err != nil {
		server.LogError("发送CONNECT响应失败: %v", err)
		return err
	}
	if err := writer.Flush(); err != nil {
		server.LogError("刷新CONNECT响应失败: %v", err)
		return err
	}

	// 读取下一个字节来判断后续的协议类型
	firstByte, err := reader.Peek(1)
	if err != nil {
		server.LogError("读取第一个字节失败: %v", err)
		return err
	}

	// 根据第一个字节判断协议类型
	switch firstByte[0] {
	case TLSHandshakeRecordType: // TLS握手记录类型
		server.LogDebug("检测到TLS握手：0x%02x", firstByte[0])
		p.isHttps = true
		return p.handleTlsHandshake(server, reader)
	case HTTPGetByte, HTTPPostByte: // 'G' (GET) 或 'P' (POST) - HTTP请求
		server.LogDebug("检测到HTTP请求：0x%02x", firstByte[0])
		p.isHttps = false
		// 递归调用handleHttpProtocol处理HTTP请求
		return p.handleHttpProtocol(server, reader, writer)
	default:
		server.LogDebug("未知协议，第一个字节：0x%02x", firstByte[0])
		// 默认尝试作为TLS处理
		p.isHttps = true
		return p.handleTlsHandshake(server, reader)
	}
}

func (p *Processor) handleWebSocket(server types.Server) error {
	// 创建WebSocket处理器并委托处理
	wsProcessor := websocket.New(p.conn, p.request, p.isHttps)

	// 如果有拦截器，设置钩子执行器
	if p.interceptor != nil {
		if hookExecutor := p.interceptor.GetHookExecutor(); hookExecutor != nil {
			wsProcessor.SetHookExecutor(hookExecutor)
		}
	}

	return wsProcessor.Process(server)
}

// handleTlsHandshake 处理TLS握手
func (p *Processor) handleTlsHandshake(server types.Server, reader *bufio.Reader) error {
	tlsHandler := newTLSHandler(p)
	return tlsHandler.handleTlsHandshake(server, reader)
}

// 转发请求
func (p *Processor) handleRequest(server types.Server) error {
	request, err := p.readRequest()
	if err != nil {
		server.LogError("读取HTTP请求失败: %v", err)
		return err
	}
	p.normalizeRequestURL(request)

	// 无管道(独立测试)时退化为简单转发,保留旧行为。
	if activePipeline == nil {
		return p.forwardSimple(server, request)
	}

	return p.handleViaPipeline(server, request)
}

// readRequest 读取(或复用)客户端请求。
func (p *Processor) readRequest() (*http.Request, error) {
	if p.request != nil {
		return p.request, nil
	}
	return http.ReadRequest(p.conn.GetReader())
}

// normalizeRequestURL 补全 scheme/host 并清空 RequestURI。
func (p *Processor) normalizeRequestURL(request *http.Request) {
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
	request.RequestURI = ""
}

// forwardSimple 简单转发(无插件管道),用于独立测试场景。
func (p *Processor) forwardSimple(server types.Server, request *http.Request) error {
	resp, err := sharedHttpClient.Do(request)
	if err != nil {
		server.LogError("请求失败: %v", err)
		writer := p.conn.GetWriter()
		_, _ = writer.WriteString(BadGatewayResponse)
		return writer.Flush()
	}
	defer resp.Body.Close()
	if err := resp.Write(p.conn.GetConn()); err != nil {
		server.LogError("写入响应失败: %v", err)
		return err
	}
	return nil
}

// handleViaPipeline 经新的 flow 管道处理:构造 Flow → 请求插件 → 转发/mock/abort/断点 → 响应插件 → 写回。
func (p *Processor) handleViaPipeline(server types.Server, request *http.Request) error {
	protocol := flow.ProtoHTTP
	if p.isHttps {
		protocol = flow.ProtoHTTPS
	}

	// 构造 Flow(读取并解码请求体,修复历史上 body 修改被丢弃的问题)。
	f := flow.BuildRequestFlow(request, protocol)
	if conn := p.conn.GetConn(); conn != nil {
		f.Request.ClientIP = conn.RemoteAddr().String()
	}

	ctx := context.Background()
	if flowSink != nil {
		flowSink.RecordFlowStarted(f)
	}

	// 请求阶段插件。
	d := activePipeline.OnRequest(ctx, f)
	switch d.Kind {
	case flow.Abort:
		f.State = flow.StateBlocked
		p.writeAbort(d)
		p.finish(f)
		return nil
	case flow.Mock:
		f.State = flow.StateMocked
		f.Timing.DurationMs = time.Since(f.Timing.RequestAt).Milliseconds()
		activePipeline.OnResponse(ctx, f) // 让插件/抓包看到 mock 响应
		return p.writeFlowResponse(server, f, request)
	}

	// 继续:把(可能改过的)Flow 应用回 request,修正长度/编码。
	_ = flow.ApplyRequestToHTTP(f, request)

	f.State = flow.StateAwaitingResponse
	resp, err := sharedHttpClient.Do(request)
	if err != nil {
		server.LogError("请求失败: %v", err)
		f.State = flow.StateErrored
		f.Error = err.Error()
		p.finish(f)
		writer := p.conn.GetWriter()
		_, _ = writer.WriteString(BadGatewayResponse)
		return writer.Flush()
	}
	defer resp.Body.Close()

	f.Timing.ResponseAt = time.Now()
	flow.CaptureResponseToFlow(f, resp)
	f.Timing.DurationMs = time.Since(f.Timing.RequestAt).Milliseconds()
	f.State = flow.StateCompleted

	// 响应阶段插件。
	d2 := activePipeline.OnResponse(ctx, f)
	if d2.Kind == flow.Abort {
		f.State = flow.StateBlocked
		p.writeAbort(d2)
		p.finish(f)
		return nil
	}

	return p.writeFlowResponse(server, f, request)
}

// writeFlowResponse 从 Flow 重建响应写回客户端,并记录完成。
func (p *Processor) writeFlowResponse(server types.Server, f *flow.Flow, request *http.Request) error {
	resp := flow.BuildHTTPResponse(f, request)
	err := resp.Write(p.conn.GetConn())
	if err != nil {
		server.LogError("写入响应失败: %v", err)
	}
	p.finish(f)
	return err
}

// writeAbort 写回阻断响应(StatusOnAbort 为 0 时直接关闭连接)。
func (p *Processor) writeAbort(d flow.Decision) {
	if d.StatusOnAbort == 0 {
		return // 直接关闭(由上层 defer conn.Close 完成)
	}
	writer := p.conn.GetWriter()
	body := d.Reason
	fmt.Fprintf(writer, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		d.StatusOnAbort, http.StatusText(d.StatusOnAbort), len(body), body)
	_ = writer.Flush()
}

// finish 记录 flow 完成。
func (p *Processor) finish(f *flow.Flow) {
	if f.Timing.CompletedAt.IsZero() {
		f.Timing.CompletedAt = time.Now()
	}
	if flowSink != nil {
		flowSink.RecordFlowCompleted(f)
	}
}
