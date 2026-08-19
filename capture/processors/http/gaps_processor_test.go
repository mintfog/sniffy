// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/procinfo"
)

// gapCA 是可注入失败点的 ca.CA 替身:GetCA 可返回 nil(模拟 CA 还没生成完),
// IssueCert 可返回错误(模拟签发失败)。
type gapCA struct {
	cert     *x509.Certificate
	issueErr error
}

func (c *gapCA) GetCA() *x509.Certificate { return c.cert }
func (c *gapCA) GetCAKey() any            { return nil }
func (c *gapCA) IssueCert(string) (*tls.Certificate, error) {
	if c.issueErr != nil {
		return nil, c.issueErr
	}
	return nil, errors.New("gapCA 未配置签发行为")
}

// withGapCA 直接改写包级 selfCA。SetCA 对 nil 是空操作,不经这里就够不到
// 「根证书尚未配置」那条分支。
func withGapCA(t *testing.T, c ca.CA) {
	t.Helper()
	caMu.Lock()
	prev := selfCA
	selfCA = c
	caMu.Unlock()
	t.Cleanup(func() {
		caMu.Lock()
		selfCA = prev
		caMu.Unlock()
	})
}

// gapDeadlineErrConn 让 SetDeadline 失败,用于覆盖 TLS 握手前设置超时失败的分支。
type gapDeadlineErrConn struct {
	*mockConn
	err error
}

func (c *gapDeadlineErrConn) SetDeadline(time.Time) error { return c.err }

func TestUpstreamProxyURLReturnsIndependentCopy(t *testing.T) {
	previous := tunnelUpstream.Load()
	t.Cleanup(func() { tunnelUpstream.Store(previous) })

	SetUpstreamProxyURL(nil)
	if got := UpstreamProxyURL(); got != nil {
		t.Fatalf("未配置上游代理时应返回 nil,实得 %v", got)
	}

	configured, _ := url.Parse("http://user:pass@proxy.example:3128")
	SetUpstreamProxyURL(configured)

	got := UpstreamProxyURL()
	if got == nil || got.Host != "proxy.example:3128" {
		t.Fatalf("UpstreamProxyURL = %v", got)
	}
	got.Host = "mutated.example:9999"
	if again := UpstreamProxyURL(); again.Host != "proxy.example:3128" {
		t.Fatalf("调用方改动返回值污染了共享状态: %q", again.Host)
	}
}

// TestTunnelViaProxyRequestWriteFailure 连上游代理后 CONNECT 请求都发不出去时要立即失败,
// 否则调用方会拿着一条并未建成隧道的连接继续转发。
func TestTunnelViaProxyRequestWriteFailure(t *testing.T) {
	wantErr := errors.New("上游代理连接已断")
	conn := &failWriteConn{mockConn: newMockConn(""), err: wantErr}
	proxyURL, _ := url.Parse("http://proxy.example:3128")

	if err := tunnelViaProxy(conn, "origin.example:443", proxyURL, time.Second); !errors.Is(err, wantErr) {
		t.Fatalf("tunnelViaProxy = %v, want %v", err, wantErr)
	}
}

// TestSetImportedServerCertsSkipsUnparsableLeaf DER 解析不出来的证书必须被丢掉,
// 否则后续 leaf.VerifyHostname 会对着 nil 指针崩在 MITM 热路径上。
func TestSetImportedServerCertsSkipsUnparsableLeaf(t *testing.T) {
	restoreImportedServerCerts(t)

	broken := &tls.Certificate{Certificate: [][]byte{{0x30, 0x00, 0xFF}}}
	valid := makeCert(t, "", []string{"ok.example"}, nil)
	SetImportedServerCerts([]*tls.Certificate{broken, valid})

	stored := importedServerCertsPtr.Load()
	if stored == nil || len(*stored) != 1 {
		t.Fatalf("解析失败的证书未被跳过,存下 %v 张", stored)
	}
	if importedServerCertFor("ok.example:443") != valid {
		t.Fatal("合法证书应仍可命中")
	}
}

// TestTLSHandshakeSetupFailures 覆盖握手前三种失败:无根 CA、签发失败、设置超时失败。
// 前两种应额外记一条 errored Flow,让 UI 看得见握手为什么没成。
func TestTLSHandshakeSetupFailures(t *testing.T) {
	restoreImportedServerCerts(t)
	SetImportedServerCerts(nil)

	t.Run("no root CA", func(t *testing.T) {
		preserveHTTPGlobals(t)
		withGapCA(t, nil)
		sink := &collectingSink{}
		SetFlowSink(sink)

		server := newMockServer()
		conn := newMockConnection(newMockConn(""), server)
		p := New(conn).(*Processor)
		p.request = &http.Request{Method: http.MethodConnect, Host: "pinned.example:443"}

		err := p.handleTlsHandshake(server, conn.GetReader())
		if err == nil || !strings.Contains(err.Error(), "根证书尚未配置") {
			t.Fatalf("无根 CA 时应报错,实得 %v", err)
		}
		recorded := sink.last()
		if recorded == nil || recorded.State != flow.StateErrored || !strings.Contains(recorded.Error, "根证书尚未配置") {
			t.Fatalf("未记录握手失败 Flow: %+v", recorded)
		}
	})

	t.Run("issue failure", func(t *testing.T) {
		preserveHTTPGlobals(t)
		wantErr := errors.New("签发被拒")
		withGapCA(t, &gapCA{issueErr: wantErr})
		sink := &collectingSink{}
		SetFlowSink(sink)

		server := newMockServer()
		conn := newMockConnection(newMockConn(""), server)
		p := New(conn).(*Processor)
		p.request = &http.Request{Method: http.MethodConnect, Host: "pinned.example:443"}

		if err := p.handleTlsHandshake(server, conn.GetReader()); !errors.Is(err, wantErr) {
			t.Fatalf("签发失败应原样返回,实得 %v", err)
		}
		recorded := sink.last()
		if recorded == nil || recorded.Request == nil || recorded.Request.Host != "pinned.example" {
			t.Fatalf("握手失败 Flow 的主机不对: %+v", recorded)
		}
	})

	t.Run("deadline failure", func(t *testing.T) {
		wantErr := errors.New("连接已废")
		server := newMockServer()
		raw := &gapDeadlineErrConn{mockConn: newMockConn(""), err: wantErr}
		conn := newMockConnection(raw, server)
		p := New(conn).(*Processor)
		p.request = &http.Request{Method: http.MethodConnect, Host: "example.test:443"}

		if err := p.handleTlsHandshake(server, conn.GetReader()); !errors.Is(err, wantErr) {
			t.Fatalf("设置超时失败应中止握手,实得 %v", err)
		}
	})
}

// TestTLSHandshakeALPNSelectsHTTP2 客户端只通告 h2 时,握手后必须分流到 HTTP/2 服务端,
// 而不是按 HTTP/1.1 继续解析(那会把 h2 前导当成请求行)。
func TestTLSHandshakeALPNSelectsHTTP2(t *testing.T) {
	restoreImportedServerCerts(t)
	SetImportedServerCerts(nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	negotiated := make(chan string, 1)
	go func() {
		c, derr := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		})
		if derr != nil {
			negotiated <- "dial error: " + derr.Error()
			return
		}
		negotiated <- c.ConnectionState().NegotiatedProtocol
		// 客户端不发 h2 前导直接断开,serveHTTP2 的 ServeConn 随即返回。
		_ = c.Close()
	}()

	raw, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept 失败: %v", err)
	}
	defer raw.Close()

	server := newMockServer()
	conn := newMockConnection(raw, server)
	p := New(conn).(*Processor)
	p.request = &http.Request{Method: http.MethodConnect, Host: "127.0.0.1"}

	if err := p.handleTlsHandshake(server, conn.GetReader()); err != nil {
		t.Fatalf("h2 分流不应报错: %v", err)
	}
	if got := <-negotiated; got != "h2" {
		t.Fatalf("ALPN 协商结果 = %q,期望 h2", got)
	}
	if _, ok := p.conn.GetConn().(*tls.Conn); !ok {
		t.Fatalf("握手后连接未被替换为 TLS 连接: %T", p.conn.GetConn())
	}
}

// TestServeIOSProfileWithoutUsableCA CA 还没就绪时,证书描述文件必须回 503,
// 而不是把 nil 证书塞进 Mobileconfig。
func TestServeIOSProfileWithoutUsableCA(t *testing.T) {
	cases := []struct {
		name string
		root ca.CA
	}{
		{"nil CA", nil},
		{"CA without certificate", &gapCA{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withGapCA(t, tc.root)
			server := newMockServer()

			raw := newMockConn("")
			p := New(newMockConnection(raw, server)).(*Processor)
			if err := p.serveIOSProfile(server); err != nil {
				t.Fatalf("serveIOSProfile: %v", err)
			}
			if !strings.Contains(raw.WrittenData(), "503 Service Unavailable") {
				t.Fatalf("h1 应回 503,实得 %q", raw.WrittenData())
			}

			recorder := httptest.NewRecorder()
			serveIOSProfileH2(server, recorder)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("h2 应回 503,实得 %d", recorder.Code)
			}
			if recorder.Header().Get("Content-Type") == "application/x-apple-aspen-config" {
				t.Fatal("503 不应带描述文件的 Content-Type")
			}
		})
	}
}

func TestTimeoutOfNilServer(t *testing.T) {
	if got := readTimeoutOf(nil); got != 0 {
		t.Fatalf("readTimeoutOf(nil) = %v", got)
	}
	if got := writeTimeoutOf(nil); got != 0 {
		t.Fatalf("writeTimeoutOf(nil) = %v", got)
	}
}

// TestHandleConnectResponseWriteFailures 「200 Connection Established」写不出去时必须立刻
// 放弃:继续往下会在一条客户端并不认为已建立的隧道上做 TLS 终止。
func TestHandleConnectResponseWriteFailures(t *testing.T) {
	wantErr := errors.New("客户端已断开")

	// 缓冲区小于状态行 → bufio 直写底层连接,错误从 WriteString 返回。
	t.Run("write", func(t *testing.T) {
		server := newMockServer()
		raw := &failWriteConn{mockConn: newMockConn(""), err: wantErr}
		conn := &mockConnection{conn: raw, reader: bufio.NewReader(raw), writer: bufio.NewWriterSize(raw, 8), server: server}
		p := New(conn).(*Processor)
		p.request = &http.Request{Method: http.MethodConnect, Host: "example.test:443"}

		if err := p.handleConnect(server, conn.GetReader(), conn.GetWriter()); !errors.Is(err, wantErr) {
			t.Fatalf("写 CONNECT 响应失败应返回错误,实得 %v", err)
		}
	})

	// 缓冲区放得下状态行 → 错误推迟到 Flush。
	t.Run("flush", func(t *testing.T) {
		server := newMockServer()
		raw := &failWriteConn{mockConn: newMockConn(""), err: wantErr}
		conn := newMockConnection(raw, server)
		p := New(conn).(*Processor)
		p.request = &http.Request{Method: http.MethodConnect, Host: "example.test:443"}

		if err := p.handleConnect(server, conn.GetReader(), conn.GetWriter()); !errors.Is(err, wantErr) {
			t.Fatalf("刷 CONNECT 响应失败应返回错误,实得 %v", err)
		}
	})
}

// TestHandleRequestReadFailure 处理器未缓存请求时应现读,读坏了要报错而不是转发半截请求。
func TestHandleRequestReadFailure(t *testing.T) {
	server := newMockServer()
	raw := newMockConn("这不是一个 HTTP 请求\r\n\r\n")
	conn := newMockConnection(raw, server)
	p := New(conn).(*Processor)

	if err := p.handleRequest(server); err == nil {
		t.Fatal("畸形请求应报错")
	}
	if raw.WrittenData() != "" {
		t.Fatalf("读请求失败时不该写回任何字节: %q", raw.WrittenData())
	}
}

// TestForwardSimpleClientWriteFailure 简单转发路径下写回客户端失败要原样上抛,
// 让上层关掉这条连接。
func TestForwardSimpleClientWriteFailure(t *testing.T) {
	preserveHTTPGlobals(t)
	wantErr := errors.New("客户端写失败")
	sharedHttpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader("upstream")),
			ContentLength: 8,
			Request:       req,
		}, nil
	})}

	server := newMockServer()
	raw := &failWriteConn{mockConn: newMockConn(""), err: wantErr}
	p := New(newMockConnection(raw, server)).(*Processor)
	request, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)

	if err := p.forwardSimple(server, request); !errors.Is(err, wantErr) {
		t.Fatalf("forwardSimple 写失败 = %v, want %v", err, wantErr)
	}
}

// TestWriteAbortWriteFailure 阻断响应写不出去时,连接必须被标记为不可复用 ——
// 客户端此刻既没拿到响应,读指针也可能停在半截字节上。
func TestWriteAbortWriteFailure(t *testing.T) {
	wantErr := errors.New("客户端写失败")
	server := newMockServer()
	raw := &failWriteConn{mockConn: newMockConn(""), err: wantErr}
	conn := &mockConnection{conn: raw, reader: bufio.NewReader(raw), writer: bufio.NewWriterSize(raw, 16), server: server}
	p := New(conn).(*Processor)

	if err := p.writeAbort(server, flow.AbortDecision(http.StatusForbidden, "denied by rule")); !errors.Is(err, wantErr) {
		t.Fatalf("writeAbort 写失败 = %v, want %v", err, wantErr)
	}
	if !p.closeAfterResponse {
		t.Fatal("写失败后应禁用连接复用")
	}
}

// TestHandleHttpProtocolRoutesWebSocket 升级请求要在主循环里被识别并交给 WebSocket 处理器,
// 它自带双向循环,返回即代表整条连接处理结束。
func TestHandleHttpProtocolRoutesWebSocket(t *testing.T) {
	preserveHTTPGlobals(t)
	server := newMockServer()
	raw := newMockConn("GET /ws HTTP/1.1\r\nHost: 127.0.0.1:0\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	conn := newMockConnection(raw, server)
	p := New(conn).(*Processor)

	if err := p.handleHttpProtocol(server, conn.GetReader(), conn.GetWriter()); err != nil {
		t.Fatalf("WebSocket 委托失败应被转成响应,实得 %v", err)
	}
	if !strings.Contains(raw.WrittenData(), "502 Bad Gateway") {
		t.Fatalf("上游不可达时应回 502,实得 %q", raw.WrittenData())
	}
}

// TestPeekHeaderLFOnlyAndMalformedLine 仅用 LF 的实现要能解析,不带冒号的行直接丢掉
// (否则会拼出一个空名头污染保真回放)。
func TestPeekHeaderLFOnlyAndMalformedLine(t *testing.T) {
	raw := "GET /p HTTP/1.1\nHost: h.example\nGarbageWithoutColon\nX-Ok: 1\n\n"
	br := bufio.NewReaderSize(strings.NewReader(raw), 64*1024)

	got := peekRequestHeaderOrder(br)
	want := [][2]string{{"Host", "h.example"}, {"X-Ok", "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LF-only 头块解析 = %#v, want %#v", got, want)
	}
}

// TestAsyncResolveProcessAttachesInfo 进程补全走独立 goroutine:解析出结果才挂到 flow 上
// 并推一次更新,解析不出来则静默。用一条真实的本机 TCP 连接喂地址,并先同步解析一次,
// 使异步那次命中缓存、结果确定。
func TestAsyncResolveProcessAttachesInfo(t *testing.T) {
	preserveHTTPGlobals(t)

	resolver := procinfo.NewResolver()
	if resolver == nil {
		t.Skip("本平台无法创建进程检测器")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer client.Close()
	accepted, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept 失败: %v", err)
	}
	defer accepted.Close()

	clientAddr, proxyAddr := accepted.RemoteAddr(), accepted.LocalAddr()
	want := resolver.Resolve(clientAddr, proxyAddr) // 预热缓存,异步那次结果与此一致

	updates := make(chan *flow.Flow, 1)
	SetFlowSink(&gapUpdateSink{updates: updates})
	SetProcessResolver(resolver)

	f := flow.New(flow.ProtoHTTP)
	asyncResolveProcess(f, clientAddr, proxyAddr)

	if want == nil {
		select {
		case got := <-updates:
			t.Fatalf("解析不到进程时不该推更新: %+v", got.Process())
		case <-time.After(200 * time.Millisecond):
		}
		return
	}
	select {
	case got := <-updates:
		if got != f {
			t.Fatalf("推送的 flow 不是原对象")
		}
		if pi := f.Process(); pi == nil || pi.PID != want.PID {
			t.Fatalf("进程信息未挂到 flow 上: %+v, want pid %d", pi, want.PID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待进程补全推送超时")
	}
}

// gapUpdateSink 只关心 RecordFlowUpdated。
type gapUpdateSink struct {
	updates chan *flow.Flow
}

func (s *gapUpdateSink) RecordFlowStarted(*flow.Flow)   {}
func (s *gapUpdateSink) RecordFlowCompleted(*flow.Flow) {}
func (s *gapUpdateSink) RecordFlowUpdated(f *flow.Flow) {
	select {
	case s.updates <- f:
	default:
	}
}
