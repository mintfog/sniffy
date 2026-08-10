// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
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

// selfCA 由引擎在启动时注入，并可在运行时替换。
// 它在每次 TLS 握手时被并发读取，故用 caMu 保护以避免与写入竞态。
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

// sharedStreamClient 与 sharedHttpClient 共享 Transport,但 Timeout=0 —— 供长连接的
// 流式转发(SSE / gRPC)使用,避免 10 分钟总超时把长流强杀。由 SetUpstreamClient 同步重建。
var sharedStreamClient *http.Client

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
	// 初始化共享的HTTP客户端，配置连接池
	sharedHttpClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // 忽略HTTPS证书
			},
			// 自定义 TLSClientConfig 会让 net/http 默认禁用 HTTP/2;显式开启,
			// 使代理可对 h2(乃至 h2-only 的 gRPC)源站协商 HTTP/2 并捕获其响应/尾部。
			ForceAttemptHTTP2: true,
			// 忠实转发:不让 Go 给没带 Accept-Encoding 的请求注入 gzip(否则上游会看到
			// 客户端没发过的头,破坏签名/防篡改校验)。与引擎自建上游客户端保持一致。
			DisableCompression: true,
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
	sharedStreamClient = streamClientFrom(sharedHttpClient)
}

// streamClientFrom 从一个上游客户端派生「无总超时」的流式客户端(共享 Transport 与重定向策略)。
func streamClientFrom(c *http.Client) *http.Client {
	if c == nil {
		return nil
	}
	return &http.Client{
		Transport:     c.Transport,
		CheckRedirect: c.CheckRedirect,
		Jar:           c.Jar,
		Timeout:       0, // 流式长连接:不设总超时
	}
}

// SetCA 注入由引擎层持有的 CA。传入 nil 时保留现有值。
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
		sharedStreamClient = streamClientFrom(c)
	}
}

// Processor HTTP协议处理器
type Processor struct {
	conn        types.Connection
	request     *http.Request
	isHttps     bool
	interceptor *RequestInterceptor

	// closeAfterResponse 表示当前请求处理完后不能继续复用客户端连接。它覆盖无法从
	// request.Close 推导出的关闭场景:代理自己生成的无响应体阻断、请求体读到一半失败
	// (读指针停在 body 中间)、以及已宣告 Content-Length 却写不满的截断响应。
	closeAfterResponse bool
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
	server.LogDebug("处理HTTP协议...")

	// HTTP/1.x 的连接默认是持久连接。顺序处理同一客户端连接上的后续请求，既支持
	// keep-alive，也自然支持已经缓冲到 reader 中的流水线请求。CONNECT/WebSocket
	// 会接管整条连接，因此不进入下一轮。
	handled := false
	for {
		// 等待下一个请求头：ReadTimeout 在此充当 keep-alive 的空闲期限。
		p.armReadDeadline(server)
		request, err := readRequestPreservingOrder(reader)
		if err != nil {
			// 客户端在一次完整往返后主动关闭，或 keep-alive 空闲超时，都是连接的正常
			// 收尾；不要把它们记录成协议错误。首个请求读坏仍向调用方报告。
			if handled && expectedClientReadEnd(err) {
				return nil
			}
			server.LogError("读取HTTP请求失败: %v", err)
			return err
		}

		p.request = request
		p.closeAfterResponse = false
		server.LogDebug("请求的域名是：" + request.Host)

		// CONNECT 后续会变成 TLS/h2 或盲转发；WebSocket 也拥有自己的双向循环。
		// 两者返回即代表整条连接处理结束，不能再按普通 HTTP 读取。
		if request.Method == http.MethodConnect {
			server.LogDebug("处理CONNECT请求")
			return p.handleConnect(server, reader, writer)
		}
		if websocket.IsWebSocketRequest(request) {
			server.LogDebug("处理WebSocket请求")
			return p.handleWebSocket(server)
		}

		body := p.guardRequestBody(server, request)
		err = p.handleRequest(server)
		// 请求体读到一半失败：读指针停在 body 中间，剩余字节会被当成下一个请求解析。
		if body != nil && body.readErr != nil {
			p.closeAfterResponse = true
		}
		if err != nil {
			return err
		}
		handled = true
		if request.Close || p.closeAfterResponse {
			return nil
		}
	}
}

// armReadDeadline 把客户端连接的读期限续到 now+ReadTimeout（未配置则不设）。
func (p *Processor) armReadDeadline(server types.Server) {
	if conn := p.conn.GetConn(); conn != nil {
		if d := readTimeoutOf(server); d > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(d))
		}
	}
}

// armWriteDeadline 在写回客户端前把写期限续到 now+WriteTimeout（未配置则不设）。
// 连接可复用后，TLS 握手期那个覆盖读写的绝对期限已被清除；没有它兜底，一个收下响应头
// 后就不再读的客户端会把本 goroutine 连同它占用的上游连接永久挂住。
func (p *Processor) armWriteDeadline(server types.Server) {
	if conn := p.conn.GetConn(); conn != nil {
		deadline := time.Time{}
		if d := writeTimeoutOf(server); d > 0 {
			deadline = time.Now().Add(d)
		}
		_ = conn.SetWriteDeadline(deadline)
	}
}

func readTimeoutOf(server types.Server) time.Duration {
	if server == nil {
		return 0
	}
	if cfg := server.GetConfig(); cfg != nil {
		return cfg.GetReadTimeout()
	}
	return 0
}

func writeTimeoutOf(server types.Server) time.Duration {
	if server == nil {
		return 0
	}
	if cfg := server.GetConfig(); cfg != nil {
		return cfg.GetWriteTimeout()
	}
	return 0
}

// deadlineConnWriter 在每次写出前把写期限续到 now+timeout。响应体可以大到一个固定期限
// 装不下（缓冲路径上限 2MiB，慢客户端未必写得完），故按写次数续期而非给整次响应一个
// 绝对期限；同时又不至于让停止读取的客户端把 goroutine 永久挂住。
type deadlineConnWriter struct {
	conn    net.Conn
	timeout time.Duration
}

func (w *deadlineConnWriter) Write(p []byte) (int, error) {
	if w.timeout > 0 {
		_ = w.conn.SetWriteDeadline(time.Now().Add(w.timeout))
	}
	return w.conn.Write(p)
}

// clientWriter 返回写回客户端的裸连接写入器（带逐次续期的写期限）。
func (p *Processor) clientWriter(server types.Server) *deadlineConnWriter {
	return &deadlineConnWriter{conn: p.conn.GetConn(), timeout: writeTimeoutOf(server)}
}

// clientRequestBody 包装客户端请求体：每次读取前把读期限续到 now+ReadTimeout，并记住
// 首个读取错误。
//
// ReadTimeout 是给「等下一个请求」用的空闲期限，直接拿它约束整个请求体会把上传总时长
// 也卡死在同一个值上（默认 30s，大文件必然被腰斩）。改成逐次续期后，慢但持续的上传不
// 受限，真正停滞的上传仍会被掐断。
type clientRequestBody struct {
	rc      io.ReadCloser
	conn    net.Conn
	timeout time.Duration
	readErr error
}

func (b *clientRequestBody) Read(p []byte) (int, error) {
	if b.timeout > 0 {
		_ = b.conn.SetReadDeadline(time.Now().Add(b.timeout))
	}
	n, err := b.rc.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && b.readErr == nil {
		b.readErr = err
	}
	return n, err
}

func (b *clientRequestBody) Close() error { return b.rc.Close() }

// guardRequestBody 就地把 request.Body 换成 clientRequestBody；无 body 或拿不到连接时
// 返回 nil。返回值供调用方在处理结束后判断请求体是否读全。
func (p *Processor) guardRequestBody(server types.Server, request *http.Request) *clientRequestBody {
	conn := p.conn.GetConn()
	if conn == nil || request.Body == nil || request.Body == http.NoBody {
		return nil
	}
	b := &clientRequestBody{rc: request.Body, conn: conn, timeout: readTimeoutOf(server)}
	request.Body = b
	return b
}

// expectedClientReadEnd 判断处理过至少一个请求后，继续读取 keep-alive 连接时遇到的
// 正常结束。半截请求导致的 UnexpectedEOF 不在此列，仍作为协议错误返回。
func expectedClientReadEnd(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// handleConnect 专门处理CONNECT请求
func (p *Processor) handleConnect(server types.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	server.LogDebug("处理CONNECT请求，目标地址：%s", p.request.Host)

	// 发送CONNECT响应，告诉客户端连接已建立
	p.armWriteDeadline(server)
	if _, err := writer.WriteString(ConnectEstablishedResponse); err != nil {
		server.LogError("发送CONNECT响应失败: %v", err)
		return err
	}
	if err := writer.Flush(); err != nil {
		server.LogError("刷新CONNECT响应失败: %v", err)
		return err
	}
	// CONNECT 已接管整条连接。清除 keep-alive 阶段的读写期限；后续 TLS 握手、盲隧道
	// 或明文内层 HTTP 会分别建立自己的期限策略。
	if conn := p.conn.GetConn(); conn != nil {
		_ = conn.SetDeadline(time.Time{})
	}

	// 解密范围:目标主机不在范围内(或 MITM 总开关关闭)则直通盲转发,不做 TLS 终止与抓包。
	if !shouldDecrypt(p.request.Host) {
		server.LogDebug("目标 %s 不在解密范围，直通转发", p.request.Host)
		return p.tunnel(server, reader)
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
	p.closeAfterResponse = true
	p.armWriteDeadline(server)
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
	return readRequestPreservingOrder(p.conn.GetReader())
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
	// forwardSimple 直接使用 resp.Write；若它向客户端写出了 Connection: close，当前
	// 处理器也必须在响应后停止读取，避免宣告关闭后又复用同一连接。注意 resp.Write 在
	// 「长度未知且非 chunked」时会在自己的副本上补写 Connection: close，而入参 resp 的
	// Close 仍为 false，故这里同口径再判一次。
	p.closeAfterResponse = resp.Close ||
		(resp.ContentLength < 0 && !slices.Contains(resp.TransferEncoding, "chunked"))
	if err := resp.Write(p.clientWriter(server)); err != nil {
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

// writeFlowResponse 从 Flow 写回响应给客户端(HTTP/1.x)。完成记录由 runFlowPipeline 负责。
// 捕获到上游原始响应头序列时按原样回放(顺序/大小写/状态行/编码保真),否则退化为标准写。
func (p *Processor) writeFlowResponse(server types.Server, f *flow.Flow, request *http.Request) error {
	err := flow.WriteResponse(p.clientWriter(server), f, request)
	if err != nil {
		server.LogError("写入响应失败: %v", err)
	}
	return err
}

// writeAbort 写回阻断响应(StatusOnAbort 为 0 时直接关闭连接)。
func (p *Processor) writeAbort(server types.Server, d flow.Decision) error {
	if d.StatusOnAbort == 0 {
		p.closeAfterResponse = true
		return nil // 直接关闭(由上层 defer conn.Close 完成)
	}
	p.armWriteDeadline(server)
	writer := p.conn.GetWriter()
	body := d.Reason
	if _, err := fmt.Fprintf(writer, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		d.StatusOnAbort, http.StatusText(d.StatusOnAbort), len(body), body); err != nil {
		p.closeAfterResponse = true
		return err
	}
	if err := writer.Flush(); err != nil {
		p.closeAfterResponse = true
		return err
	}
	return nil
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
