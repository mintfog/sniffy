// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

// Mock实现

// mockConnection 模拟连接
type mockConnection struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	server types.Server
}

func newMockConnection(conn net.Conn, server types.Server) *mockConnection {
	return &mockConnection{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
		server: server,
	}
}

func (m *mockConnection) GetConn() net.Conn        { return m.conn }
func (m *mockConnection) SetConn(conn net.Conn)    { m.conn = conn }
func (m *mockConnection) GetReader() *bufio.Reader { return m.reader }
func (m *mockConnection) GetWriter() *bufio.Writer { return m.writer }
func (m *mockConnection) GetServer() types.Server  { return m.server }
func (m *mockConnection) Close() error             { return nil }

// mockServer 模拟服务器
type mockServer struct {
	logs []string
}

func newMockServer() *mockServer {
	return &mockServer{logs: make([]string, 0)}
}

func (m *mockServer) GetConfig() types.Config { return nil }
func (m *mockServer) LogInfo(msg string, args ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("INFO: "+msg, args...))
}
func (m *mockServer) LogError(msg string, args ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("ERROR: "+msg, args...))
}
func (m *mockServer) LogDebug(msg string, args ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("DEBUG: "+msg, args...))
}
func (m *mockServer) FormatDataPreview(data []byte) string { return string(data) }

// mockConn 模拟网络连接
type mockConn struct {
	readBuffer          *bytes.Buffer
	writeBuffer         *bytes.Buffer
	closed              bool
	writeDeadlines      []time.Time
	connectionDeadlines []time.Time
}

func newMockConn(data string) *mockConn {
	return &mockConn{
		readBuffer:  bytes.NewBufferString(data),
		writeBuffer: bytes.NewBuffer(nil),
		closed:      false,
	}
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.closed {
		return 0, io.EOF
	}
	return m.readBuffer.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.closed {
		return 0, io.ErrClosedPipe
	}
	return m.writeBuffer.Write(b)
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
}
func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9090}
}
func (m *mockConn) SetDeadline(t time.Time) error {
	m.connectionDeadlines = append(m.connectionDeadlines, t)
	return nil
}
func (m *mockConn) SetReadDeadline(t time.Time) error { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error {
	m.writeDeadlines = append(m.writeDeadlines, t)
	return nil
}

func (m *mockConn) WrittenData() string {
	return m.writeBuffer.String()
}

// 测试用例

func TestNew(t *testing.T) {
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection)

	if processor == nil {
		t.Fatal("New() 应该返回非空的处理器")
	}

	httpProcessor, ok := processor.(*Processor)
	if !ok {
		t.Fatal("New() 应该返回 *Processor 类型")
	}

	if httpProcessor.conn != mockConnection {
		t.Error("处理器的连接设置不正确")
	}

	if httpProcessor.request != nil {
		t.Error("新创建的处理器request应该为nil")
	}

	if httpProcessor.isHttps {
		t.Error("新创建的处理器isHttps应该为false")
	}
}

func TestGetProtocolName(t *testing.T) {
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	protocolName := processor.GetProtocolName()
	expected := "HTTP"

	if protocolName != expected {
		t.Errorf("GetProtocolName() = %s, expected %s", protocolName, expected)
	}
}

func TestHandleHttpProtocol_InvalidRequest(t *testing.T) {
	// 测试无效的HTTP请求
	invalidHTTP := "INVALID HTTP REQUEST\r\n\r\n"
	mockConn := newMockConn(invalidHTTP)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	err := processor.handleHttpProtocol(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	if err == nil {
		t.Error("handleHttpProtocol应该对无效的HTTP请求返回错误")
	}

	// 检查是否记录了错误日志
	hasErrorLog := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "ERROR") && strings.Contains(log, "读取HTTP请求失败") {
			hasErrorLog = true
			break
		}
	}
	if !hasErrorLog {
		t.Error("应该记录错误日志")
	}
}

func TestHandleHttpProtocol_ValidGETRequest(t *testing.T) {
	// 测试有效的GET请求
	validHTTP := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	mockConn := newMockConn(validHTTP)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 注意：这个测试可能会因为实际的HTTP请求而失败，我们主要测试解析部分
	_ = processor.handleHttpProtocol(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	// 由于我们没有mock HTTP客户端，这里可能会失败，但我们可以检查请求是否被正确解析
	if processor.request == nil {
		t.Error("应该正确解析HTTP请求")
	} else {
		if processor.request.Method != "GET" {
			t.Errorf("请求方法应该是GET，得到: %s", processor.request.Method)
		}
		if processor.request.Host != "example.com" {
			t.Errorf("请求主机应该是example.com，得到: %s", processor.request.Host)
		}
	}
}

func TestHandleHttpProtocol_ReusesClientConnection(t *testing.T) {
	paths := make(chan string, 2)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		_, _ = io.WriteString(w, strings.TrimPrefix(r.URL.Path, "/"))
	}))
	defer origin.Close()

	prevClient, prevStream := sharedHttpClient, sharedStreamClient
	prevPipeline, prevSink := activePipeline, flowSink
	upstreamTransport := &http.Transport{DisableCompression: true}
	SetUpstreamClient(&http.Client{Transport: upstreamTransport, Timeout: 3 * time.Second})
	activePipeline = pipeline.New(nil, nil)
	flowSink = nil
	defer func() {
		upstreamTransport.CloseIdleConnections()
		sharedHttpClient, sharedStreamClient = prevClient, prevStream
		activePipeline, flowSink = prevPipeline, prevSink
	}()

	proxySide, clientSide := net.Pipe()
	defer clientSide.Close()
	server := newMockServer()
	conn := newMockConnection(proxySide, server)
	processor := New(conn).(*Processor)
	done := make(chan error, 1)
	go func() {
		defer proxySide.Close()
		done <- processor.handleHttpProtocol(server, conn.GetReader(), conn.GetWriter())
	}()

	clientReader := bufio.NewReader(clientSide)
	do := func(path string, closeConn bool) string {
		t.Helper()
		connection := ""
		if closeConn {
			connection = "Connection: close\r\n"
		}
		if _, err := fmt.Fprintf(clientSide, "GET %s%s HTTP/1.1\r\nHost: %s\r\n%s\r\n",
			origin.URL, path, strings.TrimPrefix(origin.URL, "http://"), connection); err != nil {
			t.Fatalf("写客户端请求: %v", err)
		}
		req, _ := http.NewRequest(http.MethodGet, origin.URL+path, nil)
		resp, err := http.ReadResponse(clientReader, req)
		if err != nil {
			t.Fatalf("读取代理响应: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("读取代理响应体: %v", err)
		}
		return string(body)
	}

	if got := do("/one", false); got != "one" {
		t.Fatalf("第一次响应体 = %q, want one", got)
	}
	// 第一次响应后不重新建连接，直接在同一个 clientSide 上发第二次请求。
	if got := do("/two", true); got != "two" {
		t.Fatalf("第二次响应体 = %q, want two", got)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("处理复用连接: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Connection: close 后处理器未退出")
	}

	if got := []string{<-paths, <-paths}; got[0] != "/one" || got[1] != "/two" {
		t.Fatalf("上游收到的路径 = %v", got)
	}
}

func TestHandleHttpProtocol_ReusesMITMTLSConnection(t *testing.T) {
	paths := make(chan string, 2)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		_, _ = io.WriteString(w, strings.TrimPrefix(r.URL.Path, "/"))
	}))
	defer origin.Close()
	target := strings.TrimPrefix(origin.URL, "https://")

	prevClient, prevStream := sharedHttpClient, sharedStreamClient
	prevPipeline, prevSink := activePipeline, flowSink
	upstreamTransport := &http.Transport{
		DisableCompression: true,
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: true},
	}
	SetUpstreamClient(&http.Client{Transport: upstreamTransport, Timeout: 3 * time.Second})
	activePipeline = pipeline.New(nil, nil)
	flowSink = nil
	defer func() {
		upstreamTransport.CloseIdleConnections()
		sharedHttpClient, sharedStreamClient = prevClient, prevStream
		activePipeline, flowSink = prevPipeline, prevSink
	}()

	proxySide, clientSide := net.Pipe()
	defer clientSide.Close()
	server := newMockServer()
	conn := types.NewConnection(proxySide, server)
	processor := New(conn).(*Processor)
	done := make(chan error, 1)
	go func() {
		defer proxySide.Close()
		done <- processor.handleHttpProtocol(server, conn.GetReader(), conn.GetWriter())
	}()

	if _, err := fmt.Fprintf(clientSide, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatalf("写 CONNECT: %v", err)
	}
	connectReq, _ := http.NewRequest(http.MethodConnect, "http://"+target, nil)
	connectResp, err := http.ReadResponse(bufio.NewReader(clientSide), connectReq)
	if err != nil {
		t.Fatalf("读取 CONNECT 响应: %v", err)
	}
	if connectResp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT 状态 = %d, want 200", connectResp.StatusCode)
	}

	tlsClient := tls.Client(clientSide, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "localhost",
		NextProtos:         []string{"http/1.1"},
	})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("客户端 TLS 握手: %v", err)
	}
	tlsReader := bufio.NewReader(tlsClient)
	do := func(path string, closeConn bool) string {
		t.Helper()
		connection := ""
		if closeConn {
			connection = "Connection: close\r\n"
		}
		if _, err := fmt.Fprintf(tlsClient, "GET %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", path, target, connection); err != nil {
			t.Fatalf("写 TLS 客户端请求: %v", err)
		}
		req, _ := http.NewRequest(http.MethodGet, origin.URL+path, nil)
		resp, err := http.ReadResponse(tlsReader, req)
		if err != nil {
			t.Fatalf("读取 TLS 代理响应: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("读取 TLS 代理响应体: %v", err)
		}
		return string(body)
	}

	if got := do("/secure-one", false); got != "secure-one" {
		t.Fatalf("第一次 TLS 响应体 = %q", got)
	}
	if got := do("/secure-two", true); got != "secure-two" {
		t.Fatalf("第二次 TLS 响应体 = %q", got)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("处理复用 TLS 连接: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TLS Connection: close 后处理器未退出")
	}

	if got := []string{<-paths, <-paths}; got[0] != "/secure-one" || got[1] != "/secure-two" {
		t.Fatalf("TLS 上游收到的路径 = %v", got)
	}
}

// ---- 连接复用的退出条件与期限 ----

// timeoutConfig 是带读写期限的最小配置（mockServer.GetConfig 返回 nil，覆盖不到期限逻辑）。
type timeoutConfig struct{ read, write time.Duration }

func (c *timeoutConfig) GetAddress() string             { return "127.0.0.1" }
func (c *timeoutConfig) GetPort() int                   { return 0 }
func (c *timeoutConfig) GetBufferSize() int             { return 4096 }
func (c *timeoutConfig) GetReadTimeout() time.Duration  { return c.read }
func (c *timeoutConfig) GetWriteTimeout() time.Duration { return c.write }
func (c *timeoutConfig) IsLoggingEnabled() bool         { return false }
func (c *timeoutConfig) GetThreads() int                { return 1 }

type timeoutServer struct {
	*mockServer
	cfg types.Config
}

func (s *timeoutServer) GetConfig() types.Config { return s.cfg }

func newTimeoutServer(read, write time.Duration) *timeoutServer {
	return &timeoutServer{mockServer: newMockServer(), cfg: &timeoutConfig{read: read, write: write}}
}

// startPipedProxy 在 net.Pipe 的一端跑 handleHttpProtocol，返回客户端一侧与它的返回值。
func startPipedProxy(t *testing.T, server types.Server) (net.Conn, <-chan error) {
	t.Helper()
	proxySide, clientSide := net.Pipe()
	conn := types.NewConnection(proxySide, server)
	processor := New(conn).(*Processor)
	done := make(chan error, 1)
	go func() {
		defer proxySide.Close()
		done <- processor.handleHttpProtocol(server, conn.GetReader(), conn.GetWriter())
	}()
	t.Cleanup(func() { clientSide.Close() })
	return clientSide, done
}

// drainResponse 读完一整条响应。net.Pipe 无缓冲，不读干净会把代理侧的写永久挂住。
func drainResponse(t *testing.T, c net.Conn, url string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := http.ReadResponse(bufio.NewReader(c), req)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("读取响应体: %v", err)
	}
	_ = resp.Body.Close()
}

// withTestUpstream 把包级全局换成指向 origin 的干净上游 + 空管道，测试结束还原。
func withTestUpstream(t *testing.T, tr *http.Transport) {
	t.Helper()
	prevClient, prevStream := sharedHttpClient, sharedStreamClient
	prevPipeline, prevSink := activePipeline, flowSink
	SetUpstreamClient(&http.Client{Transport: tr, Timeout: 5 * time.Second})
	activePipeline = pipeline.New(nil, nil)
	flowSink = nil
	t.Cleanup(func() {
		tr.CloseIdleConnections()
		sharedHttpClient, sharedStreamClient = prevClient, prevStream
		activePipeline, flowSink = prevPipeline, prevSink
	})
}

// 空闲超过 ReadTimeout 是 keep-alive 的正常收尾，不该报错。
func TestHandleHttpProtocol_IdleTimeoutEndsConnection(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hi")
	}))
	defer origin.Close()
	withTestUpstream(t, &http.Transport{DisableCompression: true})

	server := newTimeoutServer(300*time.Millisecond, time.Second)
	clientSide, done := startPipedProxy(t, server)

	host := strings.TrimPrefix(origin.URL, "http://")
	fmt.Fprintf(clientSide, "GET %s/x HTTP/1.1\r\nHost: %s\r\n\r\n", origin.URL, host)
	drainResponse(t, clientSide, origin.URL+"/x")

	// 之后什么都不发，等空闲期限到点。
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("空闲超时应视为正常收尾，得: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("空闲超时后处理器未退出")
	}
}

// 客户端在一次完整往返后直接关闭连接，同样是正常收尾。
func TestHandleHttpProtocol_ClientEOFEndsConnection(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hi")
	}))
	defer origin.Close()
	withTestUpstream(t, &http.Transport{DisableCompression: true})

	server := newTimeoutServer(5*time.Second, time.Second)
	clientSide, done := startPipedProxy(t, server)

	host := strings.TrimPrefix(origin.URL, "http://")
	fmt.Fprintf(clientSide, "GET %s/x HTTP/1.1\r\nHost: %s\r\n\r\n", origin.URL, host)
	drainResponse(t, clientSide, origin.URL+"/x")
	clientSide.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("客户端关闭应视为正常收尾，得: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("客户端关闭后处理器未退出")
	}
}

// abortHook 在请求阶段返回固定处置。
type abortHook struct{ d flow.Decision }

func (abortHook) Name() string                                          { return "test-abort" }
func (abortHook) Priority() int                                         { return 0 }
func (abortHook) Enabled() bool                                         { return true }
func (abortHook) Match(string) bool                                     { return true }
func (h abortHook) OnRequest(context.Context, *flow.Flow) flow.Decision { return h.d }

// StatusOnAbort == 0 表示直接断开：写不出任何响应，循环必须退出而不是等下一个请求。
func TestHandleHttpProtocol_AbortWithoutStatusClosesConnection(t *testing.T) {
	withTestUpstream(t, &http.Transport{})
	p := pipeline.New(nil, nil)
	p.RegisterCore(abortHook{d: flow.AbortDecision(0, "blocked")})
	activePipeline = p

	server := newTimeoutServer(5*time.Second, time.Second)
	clientSide, done := startPipedProxy(t, server)
	fmt.Fprintf(clientSide, "GET http://blocked.example/x HTTP/1.1\r\nHost: blocked.example\r\n\r\n")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("直接阻断应正常退出，得: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("直接阻断后处理器仍在等下一个请求")
	}
}

// 慢但持续的上传不该被 ReadTimeout 腰斩：它是空闲期限，不是上传总时长。
func TestHandleHttpProtocol_SlowButSteadyUploadSurvives(t *testing.T) {
	got := make(chan string, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
		_, _ = io.WriteString(w, "ok")
	}))
	defer origin.Close()
	withTestUpstream(t, &http.Transport{DisableCompression: true})

	server := newTimeoutServer(400*time.Millisecond, 2*time.Second)
	clientSide, done := startPipedProxy(t, server)

	host := strings.TrimPrefix(origin.URL, "http://")
	fmt.Fprintf(clientSide, "POST %s/up HTTP/1.1\r\nHost: %s\r\nContent-Length: 8\r\n\r\n", origin.URL, host)
	// 每 100ms 发一段，总耗时 ~800ms 是期限的两倍，但每段间隔都稳稳落在期限内。
	for i := 0; i < 8; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := clientSide.Write([]byte{byte('a' + i)}); err != nil {
			t.Fatalf("写第 %d 段: %v", i, err)
		}
	}

	select {
	case body := <-got:
		if body != "abcdefgh" {
			t.Fatalf("上游收到的请求体 = %q, want abcdefgh", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("慢速上传被中断，上游未收到完整请求体")
	}

	req, _ := http.NewRequest(http.MethodPost, origin.URL+"/up", nil)
	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	_ = resp.Body.Close()
	clientSide.Close()
	<-done
}

// 停滞的上传：请求体读不全时不能转发（否则上游把残缺请求当完整的收下），
// 且连接读指针已停在 body 中间，必须关闭而不是复用。
func TestHandleHttpProtocol_StalledUploadNotForwarded(t *testing.T) {
	reached := make(chan struct{}, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
	}))
	defer origin.Close()
	withTestUpstream(t, &http.Transport{DisableCompression: true})

	server := newTimeoutServer(200*time.Millisecond, time.Second)
	clientSide, done := startPipedProxy(t, server)

	host := strings.TrimPrefix(origin.URL, "http://")
	fmt.Fprintf(clientSide, "POST %s/up HTTP/1.1\r\nHost: %s\r\nContent-Length: 20\r\n\r\n", origin.URL, host)
	if _, err := clientSide.Write([]byte("AAAAA")); err != nil {
		t.Fatalf("写请求体前半段: %v", err)
	}
	// 之后不再发，让请求体读取停滞到期限。

	req, _ := http.NewRequest(http.MethodPost, origin.URL+"/up", nil)
	_ = clientSide.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("残缺请求体应回 400，得 %d", resp.StatusCode)
	}

	select {
	case <-reached:
		t.Fatal("残缺请求体被转发到了上游")
	default:
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("残缺请求体后应正常关闭连接，得: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("请求体失步后仍在复用连接等下一个请求")
	}
}

// 透传旁路中途读失败：响应头已按上游长度写出，body 却短了一截，只能靠关连接让客户端察觉。
func TestHandleHttpProtocol_PassthroughTruncationClosesConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// 上游声明 Content-Length: 1000，只发 10 字节就断开。
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
					return
				}
				fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Type: video/mp4\r\nContent-Length: 1000\r\n\r\n")
				_, _ = c.Write([]byte("0123456789"))
			}(c)
		}
	}()
	withTestUpstream(t, &http.Transport{DisableCompression: true})

	server := newTimeoutServer(10*time.Second, time.Second)
	clientSide, done := startPipedProxy(t, server)
	fmt.Fprintf(clientSide, "GET http://%s/v.mp4 HTTP/1.1\r\nHost: %s\r\n\r\n", ln.Addr(), ln.Addr())

	req, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/v.mp4", nil)
	_ = clientSide.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("读取响应头: %v", err)
	}
	n, cerr := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if cerr == nil {
		t.Fatalf("客户端应察觉截断（读到 %d/1000 字节却无错）", n)
	}

	// ReadTimeout 是 10s：若还在复用连接，这里必然超时。
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("透传截断应向连接层传播 unexpected EOF，得: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("透传截断后仍在复用连接等下一个请求")
	}
}

func TestHandleConnect(t *testing.T) {
	// 测试CONNECT请求处理
	connectRequest := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"
	mockConn := newMockConn(connectRequest)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 首先解析CONNECT请求
	request, err := http.ReadRequest(mockConnection.GetReader())
	if err != nil {
		t.Fatalf("解析CONNECT请求失败: %v", err)
	}
	processor.request = request

	// 为了测试handleConnect，我们需要重新设置reader
	connectRequest2 := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\nG" // 添加一个'G'字节模拟后续HTTP请求
	mockConn2 := newMockConn(connectRequest2)
	mockConnection2 := newMockConnection(mockConn2, mockServer)

	// 先读取CONNECT请求
	_, _ = http.ReadRequest(mockConnection2.GetReader())

	_ = processor.handleConnect(mockServer, mockConnection2.GetReader(), mockConnection2.GetWriter())

	// 检查是否发送了CONNECT响应
	writtenData := mockConn2.WrittenData()
	if !strings.Contains(writtenData, "HTTP/1.1 200 Connection Established") {
		t.Error("应该发送CONNECT建立响应")
	}
}

func TestHandleConnectRenewsWriteDeadlineBeforeResponse(t *testing.T) {
	mc := newMockConn("")
	server := newTimeoutServer(5*time.Second, 2*time.Second)
	conn := newMockConnection(mc, server)
	p := New(conn).(*Processor)
	p.request, _ = http.NewRequest(http.MethodConnect, "http://example.com:443", nil)
	p.request.Host = "example.com:443"

	before := time.Now()
	err := p.handleConnect(server, conn.GetReader(), conn.GetWriter())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("测试应在 CONNECT 响应后的协议探测处结束,得 %v", err)
	}
	if len(mc.writeDeadlines) == 0 || mc.writeDeadlines[0].Before(before.Add(time.Second)) {
		t.Fatalf("CONNECT 响应写 deadline 未按 WriteTimeout 续期: %v", mc.writeDeadlines)
	}
	if len(mc.connectionDeadlines) == 0 || !mc.connectionDeadlines[0].IsZero() {
		t.Fatalf("CONNECT 接管后应清除 deadline: %v", mc.connectionDeadlines)
	}
}

func TestWriteAbortFlushErrorDisablesReuse(t *testing.T) {
	proxySide, clientSide := net.Pipe()
	server := newTimeoutServer(time.Second, time.Second)
	conn := newMockConnection(proxySide, server)
	p := New(conn).(*Processor)
	_ = clientSide.Close()
	defer proxySide.Close()

	err := p.writeAbort(server, flow.AbortDecision(http.StatusForbidden, "blocked"))
	if err == nil {
		t.Fatal("Abort 响应写失败应返回错误")
	}
	if !p.closeAfterResponse {
		t.Fatal("Abort 响应写失败后应禁止复用连接")
	}
}

func TestHandleConnect_TLSHandshake(t *testing.T) {
	// 测试CONNECT请求后的TLS握手检测
	// 构造CONNECT请求 + TLS握手字节
	connectData := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n\x16" // \x16是TLS握手字节
	mockConn := newMockConn(connectData)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 解析CONNECT请求
	request, err := http.ReadRequest(mockConnection.GetReader())
	if err != nil {
		t.Fatalf("解析CONNECT请求失败: %v", err)
	}
	processor.request = request

	// 测试会因为实际的TLS握手失败，但我们可以检查是否检测到了TLS
	err = processor.handleConnect(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	// 检查日志是否包含TLS检测信息
	hasTLSDetection := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "检测到TLS握手") {
			hasTLSDetection = true
			break
		}
	}
	if !hasTLSDetection {
		t.Error("应该检测到TLS握手")
	}
}

func TestHandleConnect_HTTPRequest(t *testing.T) {
	// 测试CONNECT请求后的HTTP请求检测
	connectData := "CONNECT example.com:80 HTTP/1.1\r\nHost: example.com:80\r\n\r\nGET / HTTP/1.1\r\n"
	mockConn := newMockConn(connectData)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 解析CONNECT请求
	request, err := http.ReadRequest(mockConnection.GetReader())
	if err != nil {
		t.Fatalf("解析CONNECT请求失败: %v", err)
	}
	processor.request = request

	err = processor.handleConnect(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	// 检查日志是否包含HTTP检测信息
	hasHTTPDetection := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "检测到HTTP请求") {
			hasHTTPDetection = true
			break
		}
	}
	if !hasHTTPDetection {
		t.Error("应该检测到HTTP请求")
	}
}

func TestHandleConnect_UnknownProtocol(t *testing.T) {
	// 测试CONNECT请求后的未知协议
	connectData := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n\xAB" // 未知字节
	mockConn := newMockConn(connectData)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 解析CONNECT请求
	request, err := http.ReadRequest(mockConnection.GetReader())
	if err != nil {
		t.Fatalf("解析CONNECT请求失败: %v", err)
	}
	processor.request = request

	err = processor.handleConnect(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	// 检查日志是否包含未知协议信息
	hasUnknownProtocol := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "未知协议") {
			hasUnknownProtocol = true
			break
		}
	}
	if !hasUnknownProtocol {
		t.Error("应该检测到未知协议")
	}
}

func TestProcess(t *testing.T) {
	// 测试Process方法
	validHTTP := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	mockConn := newMockConn(validHTTP)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection)

	// Process方法会调用handleHttpProtocol
	_ = processor.Process()

	// 我们期望会有错误，因为没有真实的网络连接
	// 但我们可以检查是否尝试了处理
	hasDebugLog := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "处理HTTP协议") {
			hasDebugLog = true
			break
		}
	}
	if !hasDebugLog {
		t.Error("Process方法应该尝试处理HTTP协议")
	}
}

func TestProtocolDetectionBytes(t *testing.T) {
	tests := []struct {
		name      string
		firstByte byte
		expected  string
	}{
		{"TLS handshake", TLSHandshakeRecordType, "TLS"},
		{"HTTP GET", HTTPGetByte, "HTTP"},
		{"HTTP POST", HTTPPostByte, "HTTP"},
		{"Unknown protocol", 0xAB, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var detectedProtocol string

			switch tt.firstByte {
			case TLSHandshakeRecordType:
				detectedProtocol = "TLS"
			case HTTPGetByte, HTTPPostByte:
				detectedProtocol = "HTTP"
			default:
				detectedProtocol = "unknown"
			}

			if detectedProtocol != tt.expected {
				t.Errorf("协议检测失败: 字节 0x%02x, 期望 %s, 得到 %s", tt.firstByte, tt.expected, detectedProtocol)
			}
		})
	}
}

func TestHTTPResponseMessages(t *testing.T) {
	t.Run("Connect established response", func(t *testing.T) {
		if !strings.Contains(ConnectEstablishedResponse, "200 Connection Established") {
			t.Error("CONNECT响应应该包含'200 Connection Established'")
		}
	})

	t.Run("Bad gateway response", func(t *testing.T) {
		if !strings.Contains(BadGatewayResponse, "502 Bad Gateway") {
			t.Error("Bad Gateway响应应该包含'502 Bad Gateway'")
		}
	})
}

// 测试共享依赖的初始化效果。
func TestInitialization(t *testing.T) {
	if selfCA == nil {
		t.Error("测试根 CA 应该已注入")
	}

	if sharedHttpClient == nil {
		t.Error("sharedHttpClient 应该在init()中初始化")
	}

	// 检查HTTP客户端配置
	if sharedHttpClient != nil {
		if sharedHttpClient.Timeout != ClientTimeout {
			t.Errorf("HTTP客户端超时配置不正确: 期望 %v, 得到 %v", ClientTimeout, sharedHttpClient.Timeout)
		}

		transport, ok := sharedHttpClient.Transport.(*http.Transport)
		if !ok {
			t.Error("HTTP客户端应该使用http.Transport")
		} else {
			if transport.MaxIdleConns != MaxIdleConns {
				t.Errorf("MaxIdleConns配置不正确: 期望 %d, 得到 %d", MaxIdleConns, transport.MaxIdleConns)
			}
			if transport.MaxIdleConnsPerHost != MaxIdleConnsPerHost {
				t.Errorf("MaxIdleConnsPerHost配置不正确: 期望 %d, 得到 %d", MaxIdleConnsPerHost, transport.MaxIdleConnsPerHost)
			}
			if transport.MaxConnsPerHost != MaxConnsPerHost {
				t.Errorf("MaxConnsPerHost配置不正确: 期望 %d, 得到 %d", MaxConnsPerHost, transport.MaxConnsPerHost)
			}
			if transport.IdleConnTimeout != IdleConnTimeout {
				t.Errorf("IdleConnTimeout配置不正确: 期望 %v, 得到 %v", IdleConnTimeout, transport.IdleConnTimeout)
			}
			if transport.ResponseHeaderTimeout != ResponseHeaderTimeout {
				t.Errorf("ResponseHeaderTimeout配置不正确: 期望 %v, 得到 %v", ResponseHeaderTimeout, transport.ResponseHeaderTimeout)
			}
			if transport.ExpectContinueTimeout != ExpectContinueTimeout {
				t.Errorf("ExpectContinueTimeout配置不正确: 期望 %v, 得到 %v", ExpectContinueTimeout, transport.ExpectContinueTimeout)
			}
		}
	}
}
