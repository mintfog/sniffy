// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/processors/http/websocket"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/procinfo"
	"github.com/mintfog/sniffy/plugins"
)

// selfCA 由 init 初始化,可被 SetCA(启动注入 / 运行时重新生成 CA)替换。
// 它在每次 TLS 握手时(tls.go)被并发读取,故用 caMu 保护以避免与运行时重新生成的写入竞态。
var (
	caMu   sync.RWMutex
	selfCA ca.CA
)

// currentCA 并发安全地返回当前 CA。
func currentCA() ca.CA {
	caMu.RLock()
	defer caMu.RUnlock()
	return selfCA
}

var sharedHttpClient *http.Client

// activePipeline 是新的插件管道(基于 flow.Flow + flow.Decision)。
// 为 nil 时处理器退化为简单转发(兼容独立测试)。
var activePipeline *pipeline.Pipeline

// flowSink 接收抓到的 flow,用于写入 service(会话/统计)。
var flowSink FlowSink

// processResolver 异步解析连接对应的发起进程,补全 flow 的进程信息(可为 nil)。
var processResolver *procinfo.Resolver

// FlowSink 由 service 实现,处理器经此把 flow 写入存储(消费者定义接口,避免反向依赖)。
type FlowSink interface {
	RecordFlowStarted(f *flow.Flow)
	RecordFlowCompleted(f *flow.Flow)
	RecordFlowUpdated(f *flow.Flow)
}

// SetPipeline 注入插件管道(同时下发给 WebSocket 子处理器)。
func SetPipeline(p *pipeline.Pipeline) {
	activePipeline = p
	websocket.SetPipeline(p)
}

// SetFlowSink 注入 flow 接收器(WebSocket 会话经其 WSSink 子接口写入)。
func SetFlowSink(s FlowSink) {
	flowSink = s
	if ws, ok := s.(websocket.WSSink); ok {
		websocket.SetWSSink(ws)
	}
}

// SetProcessResolver 注入进程解析器(同时下发给 WebSocket 子处理器)。
func SetProcessResolver(r *procinfo.Resolver) {
	processResolver = r
	websocket.SetProcessResolver(r)
}

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
			// 自定义 TLSClientConfig 会让 net/http 默认禁用 HTTP/2;显式开启,
			// 使代理可对 h2(乃至 h2-only 的 gRPC)源站协商 HTTP/2 并捕获其响应/尾部。
			ForceAttemptHTTP2: true,
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
		caMu.Lock()
		selfCA = c
		caMu.Unlock()
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

	if isCertDomain(request.Host) {
		return p.serveIOSProfile(server)
	}

	p.normalizeRequestURL(request)

	// 无管道(独立测试)时退化为简单转发,保留旧行为。
	if activePipeline == nil {
		return p.forwardSimple(server, request)
	}

	return p.handleViaPipeline(server, request)
}

// certMagicDomain 是 iOS 证书安装的魔法域名：手机设好代理后 Safari 访问此域名，
// 代理直接返回 .mobileconfig，无需真实 DNS 解析。
const certMagicDomain = "cert.sniffy"

func isCertDomain(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	return h == certMagicDomain
}

// serveIOSProfile 构造并返回 .mobileconfig 配置描述文件。
func (p *Processor) serveIOSProfile(server types.Server) error {
	server.LogDebug("拦截 %s，返回 iOS 证书描述文件", certMagicDomain)
	c := currentCA()
	if c == nil {
		return p.writeRawResponse("HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n")
	}
	caCert := c.GetCA()
	if caCert == nil {
		return p.writeRawResponse("HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n")
	}
	profile := ca.Mobileconfig(caCert)
	writer := p.conn.GetWriter()
	fmt.Fprintf(writer,
		"HTTP/1.1 200 OK\r\nContent-Type: application/x-apple-aspen-config\r\nContent-Disposition: attachment; filename=sniffy.mobileconfig\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		len(profile),
	)
	_, _ = writer.Write(profile)
	return writer.Flush()
}

func (p *Processor) writeRawResponse(s string) error {
	writer := p.conn.GetWriter()
	_, _ = writer.WriteString(s)
	return writer.Flush()
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

// handleViaPipeline 经 flow 管道处理 HTTP/1.x 请求:委托给协议无关的 runFlowPipeline,
// 通过 connResponder 把结果写回 bufio / 裸连接。HTTP/2 走同一核心(见 h2.go)。
func (p *Processor) handleViaPipeline(server types.Server, request *http.Request) error {
	protocol := flow.ProtoHTTP
	if p.isHttps {
		protocol = flow.ProtoHTTPS
	}
	var clientAddr, proxyAddr net.Addr
	if conn := p.conn.GetConn(); conn != nil {
		clientAddr, proxyAddr = conn.RemoteAddr(), conn.LocalAddr()
	}
	return runFlowPipeline(server, request, protocol, clientAddr, proxyAddr, &connResponder{p: p, server: server})
}

// resolveProcessAsync 解析本连接对应的发起进程(best-effort),委托给包级 asyncResolveProcess。
func (p *Processor) resolveProcessAsync(f *flow.Flow) {
	conn := p.conn.GetConn()
	if conn == nil {
		return
	}
	asyncResolveProcess(f, conn.RemoteAddr(), conn.LocalAddr())
}

// writeFlowResponse 从 Flow 重建响应写回客户端(HTTP/1.x)。完成记录由 runFlowPipeline 负责。
func (p *Processor) writeFlowResponse(server types.Server, f *flow.Flow, request *http.Request) error {
	resp := flow.BuildHTTPResponse(f, request)
	err := resp.Write(p.conn.GetConn())
	if err != nil {
		server.LogError("写入响应失败: %v", err)
	}
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

// recordTLSFailure 把一次失败的 TLS 握手记成一条 errored Flow 上报给 UI。
// 握手失败(客户端不信任证书、协议版本不符、连接被探测后立即关闭等)
func (p *Processor) recordTLSFailure(host string, cause error) {
	if flowSink == nil || cause == nil || host == "" {
		return
	}
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	f := flow.New(flow.ProtoHTTPS)
	f.State = flow.StateErrored
	f.Error = "TLS 握手失败: " + cause.Error()
	f.Request = &flow.Request{
		Method: http.MethodConnect,
		URL:    "https://" + hostname,
		Host:   hostname,
		Proto:  "HTTP/1.1",
		Header: map[string][]string{},
	}
	if conn := p.conn.GetConn(); conn != nil {
		f.Request.ClientIP = conn.RemoteAddr().String()
	}
	now := time.Now()
	f.Timing.CompletedAt = now
	f.Timing.DurationMs = now.Sub(f.Timing.RequestAt).Milliseconds()

	// f 在此之后不再被本 goroutine 改动,故与 resolveProcessAsync 内异步读取无竞态。
	flowSink.RecordFlowStarted(f)
	p.resolveProcessAsync(f)
	flowSink.RecordFlowCompleted(f)
}
