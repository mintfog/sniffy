// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/forward"
	"github.com/mintfog/sniffy/internal/pipeline"
)

func TestProxyAuthorization(t *testing.T) {
	SetProxyAuth(true, "sniffy", "s3cret")
	t.Cleanup(func() { SetProxyAuth(false, "", "") })

	valid, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	valid.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("sniffy:s3cret")))
	if !checkProxyAuthorization(valid) {
		t.Fatal("valid proxy credentials rejected")
	}

	for name, value := range map[string]string{
		"missing":   "",
		"wrong":     "Basic " + base64.StdEncoding.EncodeToString([]byte("sniffy:wrong")),
		"nonbasic":  "Bearer token",
		"malformed": "Basic !!!",
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
			r.Header.Set("Proxy-Authorization", value)
			if checkProxyAuthorization(r) {
				t.Fatal("invalid proxy credentials accepted")
			}
		})
	}
}

func TestProxyAuthChallenge(t *testing.T) {
	var b strings.Builder
	w := &testStringFlusher{Builder: &b}
	if err := writeProxyAuthChallenge(w); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "407 Proxy Authentication Required") ||
		!strings.Contains(got, "Proxy-Authenticate: Basic realm=\"Sniffy\"") ||
		!strings.Contains(got, "Connection: close") {
		t.Fatalf("challenge = %q", got)
	}

	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(got)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHTTPProcessorRejectsUnauthenticatedProxyRequest(t *testing.T) {
	SetProxyAuth(true, "sniffy", "s3cret")
	t.Cleanup(func() { SetProxyAuth(false, "", "") })

	conn := newMockConn("GET http://example.test/ HTTP/1.1\r\nHost: example.test\r\n\r\n")
	p := New(newMockConnection(conn, newMockServer())).(*Processor)
	if err := p.handleHttpProtocol(p.conn.GetServer(), p.conn.GetReader(), p.conn.GetWriter()); err != nil {
		t.Fatal(err)
	}
	got := conn.writeBuffer.String()
	if !strings.HasPrefix(got, "HTTP/1.1 407 Proxy Authentication Required\r\n") {
		t.Fatalf("response = %q", got)
	}
}

// TestProxyAuthorizationRejectsIncompleteCredentials 锁定 fail-closed:认证开着但账号或
// 密码为空时,`Basic dXNlcjo=` / `Basic Og==` 这类空密码猜测必须被拒,而不是放行。
func TestProxyAuthorizationRejectsIncompleteCredentials(t *testing.T) {
	t.Cleanup(func() { SetProxyAuth(false, "", "") })

	for name, tc := range map[string]struct{ username, password, guess string }{
		"空密码":   {"sniffy", "", "sniffy:"},
		"空账号":   {"", "s3cret", ":s3cret"},
		"两者皆空":  {"", "", ":"},
		"两者皆空猜": {"", "", "sniffy:s3cret"},
	} {
		t.Run(name, func(t *testing.T) {
			SetProxyAuth(true, tc.username, tc.password)
			r, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
			r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(tc.guess)))
			if checkProxyAuthorization(r) {
				t.Fatal("凭据不全时仍放行了客户端")
			}
		})
	}
}

// TestProxyAuthorizationDisabled 锁定未开启认证时不校验、不挑战:客户端带不带
// Proxy-Authorization 都照常放行。
func TestProxyAuthorizationDisabled(t *testing.T) {
	SetProxyAuth(false, "", "")

	bare, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	withCreds, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	withCreds.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("who:ever")))
	for name, r := range map[string]*http.Request{"无凭据": bare, "带无关凭据": withCreds} {
		if !checkProxyAuthorization(r) {
			t.Fatalf("%s:关闭认证后仍被拒绝", name)
		}
	}
}

// TestStripProxyAuthorization 锁定凭据在头表与原始头序列两处都被剔除 ——
// 前者决定抓包记录与普通转发,后者决定 WebSocket 保真握手写给源站的字节。
func TestStripProxyAuthorization(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	req.Header.Set("Proxy-Authorization", "Basic dXNlcjpwYXNz")
	req.Header.Set("X-Keep", "1")
	req = req.WithContext(flow.WithRawHeaders(req.Context(), [][2]string{
		{"Host", "example.test"},
		{"proxy-authorization", "Basic dXNlcjpwYXNz"}, // 小写:剔除不能依赖客户端的大小写
		{"X-Keep", "1"},
	}))

	req = stripProxyAuthorization(req)

	if got := req.Header.Get("Proxy-Authorization"); got != "" {
		t.Fatalf("头表仍残留凭据: %q", got)
	}
	raw, ok := flow.RawHeadersFrom(req.Context())
	if !ok {
		t.Fatal("原始头序列被整体丢弃,保真转发会退化")
	}
	for _, kv := range raw {
		if strings.EqualFold(kv[0], "Proxy-Authorization") {
			t.Fatalf("原始头序列仍残留凭据: %v", raw)
		}
	}
	if len(raw) != 2 || raw[0][0] != "Host" || raw[1][0] != "X-Keep" {
		t.Fatalf("剔除时破坏了其余头的顺序: %v", raw)
	}
	if req.Header.Get("X-Keep") != "1" {
		t.Fatal("剔除时误删了其它头")
	}
}

// TestProxyCredentialsNotForwardedHTTPEndToEnd 端到端锁定:认证通过后,客户端出示的
// 代理凭据既不会随请求发给源站,也不会进入抓包记录(插件 / UI / 会话导出都读它)。
func TestProxyCredentialsNotForwardedHTTPEndToEnd(t *testing.T) {
	SetProxyAuth(true, "sniffy", "s3cret")
	t.Cleanup(func() { SetProxyAuth(false, "", "") })

	addr, gotHead := startHeadRecordingUpstream(t, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")

	defer func(c *http.Client, p *pipeline.Pipeline, s FlowSink) {
		sharedHttpClient, activePipeline, flowSink = c, p, s
	}(sharedHttpClient, activePipeline, flowSink)
	sharedHttpClient = &http.Client{
		Transport: forward.New(forward.Config{Fallback: &http.Transport{}}),
		Timeout:   5 * time.Second,
	}
	activePipeline = pipeline.New(nil, nil)
	sink := &collectingSink{}
	flowSink = sink

	clientReq := "GET http://" + addr + "/p HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("sniffy:s3cret")) + "\r\n" +
		"Proxy-Connection: keep-alive\r\n" +
		"User-Agent: MyApp/1.0\r\n" +
		"\r\n"

	mc := newMockConn(clientReq)
	srv := newMockServer()
	conn := newMockConnection(mc, srv)
	p := New(conn).(*Processor)
	if err := p.handleHttpProtocol(srv, conn.GetReader(), conn.GetWriter()); err != nil {
		t.Fatalf("handleHttpProtocol: %v", err)
	}

	select {
	case head := <-gotHead:
		if strings.Contains(strings.ToLower(head), "proxy-") {
			t.Fatalf("代理专属头被转发给了源站:\n%s", head)
		}
		if !strings.Contains(head, "User-Agent: MyApp/1.0") {
			t.Fatalf("剔除凭据时破坏了其余头的保真转发:\n%s", head)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未在超时内收到请求")
	}

	f := sink.last()
	if f == nil || f.Request == nil {
		t.Fatal("未记录到 Flow")
	}
	if v := f.Request.Header["Proxy-Authorization"]; len(v) > 0 {
		t.Fatalf("抓包记录里残留代理凭据: %v", v)
	}
	for _, kv := range f.Request.RawHeaders {
		if strings.EqualFold(kv[0], "Proxy-Authorization") {
			t.Fatalf("抓包记录的原始头序列里残留代理凭据: %v", f.Request.RawHeaders)
		}
	}
}

// TestProxyCredentialsNotForwardedWebSocketEndToEnd 端到端锁定 WebSocket 侧:保真握手
// 会把客户端的原始头序列原样写给源站,凭据必须已在此之前被剔除。这里刻意关闭认证 ——
// 客户端可能带着上一跳代理的凭据,不认证不等于可以外传。
func TestProxyCredentialsNotForwardedWebSocketEndToEnd(t *testing.T) {
	SetProxyAuth(false, "", "")

	addr, gotHead := startHeadRecordingUpstream(t,
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")

	clientReq := "GET http://" + addr + "/ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("sniffy:s3cret")) + "\r\n" +
		"Proxy-Connection: keep-alive\r\n" +
		"\r\n"

	mc := newMockConn(clientReq)
	srv := newMockServer()
	conn := newMockConnection(mc, srv)
	p := New(conn).(*Processor)
	if err := p.handleHttpProtocol(srv, conn.GetReader(), conn.GetWriter()); err != nil {
		t.Fatalf("handleHttpProtocol: %v", err)
	}

	select {
	case head := <-gotHead:
		if strings.Contains(strings.ToLower(head), "proxy-") {
			t.Fatalf("代理专属头被转发给了 WebSocket 源站:\n%s", head)
		}
		if !strings.Contains(head, "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==") ||
			!strings.Contains(head, "Upgrade: websocket") ||
			!strings.Contains(head, "Connection: Upgrade") {
			t.Fatalf("剔除代理头时破坏了握手保真:\n%s", head)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未在超时内收到握手")
	}
}

// startHeadRecordingUpstream 起一个只读一个请求头块、回固定响应的上游,
// 返回其地址与承载头块原文的 channel。
func startHeadRecordingUpstream(t *testing.T, response string) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	gotHead := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		var head strings.Builder
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			head.WriteString(line)
			if line == "\r\n" {
				break
			}
		}
		gotHead <- head.String()
		_, _ = c.Write([]byte(response))
	}()
	return ln.Addr().String(), gotHead
}

// BenchmarkStripProxyAuthorization 覆盖热路径上的常见形态:客户端不带代理凭据。
// 此时不得复制请求或原始头序列,否则每请求多一次分配。
func BenchmarkStripProxyAuthorization(b *testing.B) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.test/p", nil)
	raw := [][2]string{
		{"Host", "example.test"},
		{"User-Agent", "MyApp/1.0"},
		{"Accept", "*/*"},
		{"Accept-Encoding", "gzip, br"},
		{"Cookie", "sid=xyz"},
		{"X-Request-ID", "42"},
	}
	for _, kv := range raw {
		req.Header.Set(kv[0], kv[1])
	}
	req = req.WithContext(flow.WithRawHeaders(req.Context(), raw))

	b.ReportAllocs()
	for b.Loop() {
		if stripProxyAuthorization(req) != req {
			b.Fatal("无凭据时不应复制请求")
		}
	}
}

type testStringFlusher struct{ *strings.Builder }

func (w *testStringFlusher) WriteString(s string) (int, error) { return w.Builder.WriteString(s) }
func (w *testStringFlusher) Flush() error                      { return nil }
