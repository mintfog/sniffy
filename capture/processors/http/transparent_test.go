// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/outboundtls"
)

// transparentConn 给 net.Pipe 的一端套上透明连接的地址约定。
type transparentConn struct {
	net.Conn
	local, remote net.Addr
}

func (c *transparentConn) LocalAddr() net.Addr  { return c.local }
func (c *transparentConn) RemoteAddr() net.Addr { return c.remote }

// startTransparent 在 net.Pipe 的一端跑 ServeTransparent，原始目标为 dst，返回应用一侧的连接。
func startTransparent(t *testing.T, dst net.Addr) (net.Conn, <-chan error) {
	t.Helper()
	proxySide, appSide := net.Pipe()
	conn := &transparentConn{
		Conn:   proxySide,
		local:  dst,
		remote: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 50000},
	}
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		defer proxySide.Close()
		done <- ServeTransparent(types.NewConnection(conn, newMockServer()))
	}()
	// 等处理协程退出再让后注册的清理还原包级全局，否则两者竞态。
	t.Cleanup(func() {
		appSide.Close()
		<-exited
	})
	_ = appSide.SetDeadline(time.Now().Add(10 * time.Second))
	return appSide, done
}

// withPinnedUpstream 装上按原始目标拨号的上游客户端，等价于引擎经 TCPListener.DialContext 出站。
func withPinnedUpstream(t *testing.T) {
	t.Helper()
	withTestUpstream(t, &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, types.PinnedDialAddr(ctx, address))
		},
	})
}

func resetMITMBypass(t *testing.T) {
	t.Helper()
	clearBypass := func() {
		mitmBypass.mu.Lock()
		clear(mitmBypass.hosts)
		mitmBypass.mu.Unlock()
	}
	clearBypass()
	t.Cleanup(clearBypass)
}

// bypassKeyFor 返回 host 在原始目标 dst 的端口上对应的直通名单键。
func bypassKeyFor(host string, dst net.Addr) string {
	return net.JoinHostPort(host, strconv.Itoa(dst.(*net.TCPAddr).Port))
}

func sniffyRootPool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(currentCA().GetCA())
	return pool
}

// getOver 在已建立的连接上发一个 GET 并返回响应体。
func getOver(t *testing.T, conn net.Conn, host string) string {
	t.Helper()
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host); err != nil {
		t.Fatalf("写请求: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	return string(body)
}

// TestTransparentTLSDecrypts：带 SNI 的 TLS 连接按域名签发证书并解密，出站连的是应用的原始目标
// （域名 mitm.test 无法解析，只有按原始目标拨号才到得了源站）。
func TestTransparentTLSDecrypts(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	withPinnedUpstream(t)
	origin := startOrigin(t)
	defer origin.Close()

	app, _ := startTransparent(t, origin.Addr())
	conn := tls.Client(app, &tls.Config{ServerName: "mitm.test", RootCAs: sniffyRootPool(t)})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	if body := getOver(t, conn, "mitm.test"); body != "TUNNELED" {
		t.Fatalf("响应体 = %q，期望源站的 \"TUNNELED\"", body)
	}
}

// TestTransparentTLSDecryptsHTTP2：h2 流上的请求同样带着原始目标出站。:scheme 为 http 而
// :authority 就是握手主机的请求（自己做 TLS 的 gRPC 客户端）也发给源站。
func TestTransparentTLSDecryptsHTTP2(t *testing.T) {
	for _, scheme := range []string{"https", "http"} {
		t.Run(":scheme "+scheme, func(t *testing.T) {
			restoreScopeGlobals(t)
			resetMITMBypass(t)
			SetDecryptScope(true, "all", nil, nil)
			tunnelUpstream.Store(nil)
			withPinnedUpstream(t)
			origin := startOrigin(t)
			defer origin.Close()

			app, _ := startTransparent(t, origin.Addr())
			conn := tls.Client(app, &tls.Config{ServerName: "mitm.test", RootCAs: sniffyRootPool(t), NextProtos: []string{"h2"}})
			if err := conn.Handshake(); err != nil {
				t.Fatalf("TLS 握手: %v", err)
			}
			clientConn, err := (&http2.Transport{}).NewClientConn(conn)
			if err != nil {
				t.Fatalf("建立 h2 连接: %v", err)
			}
			request, _ := http.NewRequest(http.MethodGet, scheme+"://mitm.test/", nil)
			resp, err := clientConn.RoundTrip(request)
			if err != nil {
				t.Fatalf("h2 请求: %v", err)
			}
			defer resp.Body.Close()
			if body, _ := io.ReadAll(resp.Body); string(body) != "TUNNELED" {
				t.Fatalf("响应体 = %q，期望源站的 \"TUNNELED\"", body)
			}
			if mitmBypass.active(bypassKeyFor("mitm.test", origin.Addr())) {
				t.Fatal("发给源站的请求不应让主机进直通名单")
			}
		})
	}
}

// TestTransparentTLSWithoutSNI：没有 SNI 时按原始目标 IP 签发证书并解密。
func TestTransparentTLSWithoutSNI(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	withPinnedUpstream(t)
	origin := startOrigin(t)
	defer origin.Close()

	app, _ := startTransparent(t, origin.Addr())
	// ServerName 是 IP 时 crypto/tls 不发 SNI，同时仍按该 IP 校验证书。
	conn := tls.Client(app, &tls.Config{ServerName: "127.0.0.1", RootCAs: sniffyRootPool(t)})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	if body := getOver(t, conn, origin.Addr().String()); body != "TUNNELED" {
		t.Fatalf("响应体 = %q，期望源站的 \"TUNNELED\"", body)
	}
}

// originIssuer 以给定配置经透明入口握手，返回应用所见证书的签发者。
func originIssuer(t *testing.T, dst net.Addr, config *tls.Config) string {
	t.Helper()
	app, _ := startTransparent(t, dst)
	config.InsecureSkipVerify = true
	conn := tls.Client(app, config)
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	return conn.ConnectionState().PeerCertificates[0].Issuer.CommonName
}

// TestTransparentTLSBlindForward：不解密的 TLS 连接原样转发到原始目标，应用看到的是源站自己的证书。
func TestTransparentTLSBlindForward(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	tunnelUpstream.Store(nil)
	origin := startOrigin(t)
	defer origin.Close()

	t.Run("域名在解密范围外", func(t *testing.T) {
		SetDecryptScope(true, "deny", nil, []string{"skip.test"})
		if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "skip.test"}); issuer != originCN {
			t.Fatalf("签发者 = %q，期望源站 %q", issuer, originCN)
		}
	})
	t.Run("ALPN 不含 HTTP", func(t *testing.T) {
		SetDecryptScope(true, "all", nil, nil)
		if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "mail.test", NextProtos: []string{"imap"}}); issuer != originCN {
			t.Fatalf("签发者 = %q，期望源站 %q", issuer, originCN)
		}
	})
}

// TestTransparentTLSBypassAfterRejection：应用拒绝 sniffy 的证书后，到同一主机的后续连接改为盲转发。
func TestTransparentTLSBypassAfterRejection(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	origin := startOrigin(t)
	defer origin.Close()

	app, done := startTransparent(t, origin.Addr())
	// 空的信任库：应用不认 sniffy 的根证书，校验失败后回 bad_certificate alert。
	rejecting := tls.Client(app, &tls.Config{ServerName: "pinned.test", RootCAs: x509.NewCertPool()})
	if err := rejecting.Handshake(); err == nil {
		t.Fatal("期望握手因证书不受信任而失败")
	}
	if err := <-done; err == nil {
		t.Fatal("期望透明入口报告握手失败")
	}

	if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "pinned.test"}); issuer != originCN {
		t.Fatalf("签发者 = %q，期望源站 %q", issuer, originCN)
	}
}

func TestMITMBypassStrikes(t *testing.T) {
	alert := &net.OpError{Op: "remote error", Err: errors.New("tls: bad certificate")}

	t.Run("断开连接要连续两次才生效", func(t *testing.T) {
		list := &bypassList{hosts: make(map[string]bypassEntry)}
		list.noteFailure("a.test", io.EOF)
		if list.active("a.test") {
			t.Fatal("一次原因不明的失败不应转为盲转发")
		}
		list.noteFailure("a.test", io.EOF)
		if !list.active("a.test") {
			t.Fatal("连续两次失败后应转为盲转发")
		}
	})
	t.Run("握手成功清零", func(t *testing.T) {
		list := &bypassList{hosts: make(map[string]bypassEntry)}
		list.noteFailure("a.test", io.EOF)
		list.noteSuccess("a.test")
		list.noteFailure("a.test", io.EOF)
		if list.active("a.test") {
			t.Fatal("中间成功过一次，不应转为盲转发")
		}
	})
	t.Run("握手成功不清已生效的记录", func(t *testing.T) {
		list := &bypassList{hosts: make(map[string]bypassEntry)}
		list.add("a.test", mitmBypassStrikes)
		list.noteSuccess("a.test")
		if !list.active("a.test") {
			t.Fatal("已生效的记录应留到过期")
		}
	})
	t.Run("alert 一次生效且只影响该主机", func(t *testing.T) {
		list := &bypassList{hosts: make(map[string]bypassEntry)}
		list.noteFailure("a.test", alert)
		if !list.active("a.test") || list.active("b.test") {
			t.Fatalf("a.test = %v，b.test = %v，期望 true / false", list.active("a.test"), list.active("b.test"))
		}
	})
	t.Run("到期后重新尝试解密", func(t *testing.T) {
		list := &bypassList{hosts: map[string]bypassEntry{
			"a.test": {strikes: mitmBypassStrikes, expires: time.Now().Add(-time.Second)},
		}}
		if list.active("a.test") {
			t.Fatal("过期条目不应继续生效")
		}
		list.noteFailure("a.test", io.EOF)
		if list.active("a.test") {
			t.Fatal("过期条目的失败次数不应累计到新的一轮")
		}
	})
	t.Run("条目数有上限", func(t *testing.T) {
		list := &bypassList{hosts: make(map[string]bypassEntry)}
		for i := range mitmBypassMaxHosts + 1 {
			list.noteFailure(fmt.Sprintf("host-%d.test", i), alert)
		}
		if len(list.hosts) > mitmBypassMaxHosts {
			t.Fatalf("条目数 = %d，超过上限 %d", len(list.hosts), mitmBypassMaxHosts)
		}
	})
}

// TestTransparentPlainHTTP：明文 HTTP 进请求管道，出站连原始目标。
func TestTransparentPlainHTTP(t *testing.T) {
	restoreScopeGlobals(t)
	tunnelUpstream.Store(nil)
	withPinnedUpstream(t)
	originLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "origin saw "+r.Host)
	})}
	go func() { _ = origin.Serve(originLn) }()
	defer origin.Close()

	t.Run("域名", func(t *testing.T) {
		app, _ := startTransparent(t, originLn.Addr())
		if body := getOver(t, app, "plain.test"); body != "origin saw plain.test" {
			t.Fatalf("响应体 = %q", body)
		}
	})
	// HTTP/1.0 请求可以不带 Host，出站地址只能来自原始目标。
	t.Run("不带 Host 的 HTTP/1.0 请求", func(t *testing.T) {
		app, _ := startTransparent(t, originLn.Addr())
		if _, err := io.WriteString(app, "GET / HTTP/1.0\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(app), nil)
		if err != nil {
			t.Fatalf("读响应: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if want := "origin saw " + originLn.Addr().String(); resp.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("响应 = %d %q，期望 200 %q", resp.StatusCode, body, want)
		}
	})
	// Host 与连接本端地址相同，在代理入站连接上意味着自转发或访问入口页；透明连接的本端
	// 地址是原始目标，请求必须照常到达源站。
	t.Run("Host 即原始目标地址", func(t *testing.T) {
		app, _ := startTransparent(t, originLn.Addr())
		host := originLn.Addr().String()
		if body := getOver(t, app, host); body != "origin saw "+host {
			t.Fatalf("响应体 = %q", body)
		}
	})
}

// startBannerOrigin 启动一个先发 banner、再回显的源站，模拟由服务端先说话的协议。
func startBannerOrigin(t *testing.T, banner string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.WriteString(conn, banner)
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln
}

func expectRead(t *testing.T, conn net.Conn, want string) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("读取: %v", err)
	}
	if string(got) != want {
		t.Fatalf("读到 %q，期望 %q", got, want)
	}
}

// TestTransparentServerSpeaksFirst：应用不发首字节时，到点转为盲转发，服务端的 banner 照常到达。
func TestTransparentServerSpeaksFirst(t *testing.T) {
	restoreScopeGlobals(t)
	tunnelUpstream.Store(nil)
	origin := startBannerOrigin(t, "220 ready\r\n")
	defer origin.Close()

	app, _ := startTransparent(t, origin.Addr())
	expectRead(t, app, "220 ready\r\n")
	if _, err := io.WriteString(app, "EHLO app\r\n"); err != nil {
		t.Fatal(err)
	}
	expectRead(t, app, "EHLO app\r\n")
}

// TestTransparentUnknownProtocol：首批字节既不是 TLS 也不是 HTTP 请求时原样盲转发。
func TestTransparentUnknownProtocol(t *testing.T) {
	restoreScopeGlobals(t)
	tunnelUpstream.Store(nil)
	origin := startBannerOrigin(t, "")
	defer origin.Close()

	for _, payload := range []string{
		"\x00\x01\x02binary",
		"hi",
		"CONNECT example.com:443 HTTP/1.1\r\n\r\n",
		"GET http://example.com/ HTTP/1.1\r\n\r\n",
		"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n",
		"GET /session\r\n",
		// 0x16 开头却不是 ClientHello。
		"\x16\x03\x01\x00\x02\xff\xff",
	} {
		t.Run(fmt.Sprintf("%q", payload), func(t *testing.T) {
			app, _ := startTransparent(t, origin.Addr())
			if _, err := io.WriteString(app, payload); err != nil {
				t.Fatal(err)
			}
			expectRead(t, app, payload)
		})
	}
}

func TestSniffHTTPRequest(t *testing.T) {
	for input, want := range map[string]sniffVerdict{
		"GET / HTTP/1.1\r\n":                      sniffedHTTP,
		"GET / HTTP/1.0\n":                        sniffedHTTP,
		"POST /submit HTTP/1.1\r\nHost: a\r\n":    sniffedHTTP,
		"OPTIONS * HTTP/1.1\r\n":                  sniffedHTTP,
		"M-SEARCH * HTTP/1.1\r\n":                 sniffedHTTP,
		"UPDATEREDIRECTREF /x HTTP/1.1\r\n":       sniffedHTTP,
		"CONNECT example.com:443 HTTP/1.1\r\n":    sniffedOther,
		"GET http://example.com/ HTTP/1.1\r\n":    sniffedOther,
		"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n":        sniffedOther,
		"SSH-2.0-OpenSSH_9.6\r\n":                 sniffedOther,
		"get / HTTP/1.1\r\n":                      sniffedOther,
		" / HTTP/1.1\r\n":                         sniffedOther,
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ / HTTP/1.1\n": sniffedOther,
		// Redis inline 命令：开头像请求行，行尾没有协议版本。
		"GET /session\r\n": sniffedOther,
		// 与请求行不矛盾，但整行还没到齐。
		"G":              sniffUndecided,
		"GET":            sniffUndecided,
		"GET ":           sniffUndecided,
		"GET / HTTP/1.1": sniffUndecided,
	} {
		reader := bufio.NewReader(strings.NewReader(input))
		if got := sniffHTTPRequest(reader); got != want {
			t.Errorf("sniffHTTPRequest(%q) = %v，期望 %v", input, got, want)
		}
		if rest, _ := io.ReadAll(reader); string(rest) != input {
			t.Errorf("sniffHTTPRequest(%q) 消费了字节，剩余 %q", input, rest)
		}
	}
}

func TestWithOriginalDst(t *testing.T) {
	const dst = "192.0.2.1:8443"
	for _, tc := range []struct {
		name, url, host string
		isHTTPS         bool
		authority       string
	}{
		{name: "https 缺省端口", url: "/", host: "example.com", isHTTPS: true, authority: "example.com:443"},
		{name: "http 缺省端口", url: "/", host: "example.com", authority: "example.com:80"},
		{name: "显式端口", url: "/", host: "example.com:8443", isHTTPS: true, authority: "example.com:8443"},
		{name: "IPv6 字面量", url: "/", host: "[2001:db8::1]", isHTTPS: true, authority: "[2001:db8::1]:443"},
		{name: "URL 自带主机", url: "http://other.test:81/", host: "example.com", authority: "other.test:81"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Host = tc.host
			ctx := withOriginalDst(request, tc.isHTTPS, dst).Context()
			if got := types.PinnedDialAddr(ctx, tc.authority); got != dst {
				t.Fatalf("拨 %q 实际连 %q，期望 %q", tc.authority, got, dst)
			}
		})
	}
}

// TestTransparentWebSocket：WebSocket 升级请求的上游连接同样拨原始目标。
func TestTransparentWebSocket(t *testing.T) {
	restoreScopeGlobals(t)
	tunnelUpstream.Store(nil)
	SetDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, types.PinnedDialAddr(ctx, address))
	})
	t.Cleanup(func() { SetDialContext(nil) })

	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		conn, err := origin.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_, _ = io.Copy(io.Discard, conn)
	}()

	app, _ := startTransparent(t, origin.Addr())
	if _, err := io.WriteString(app, "GET /ws HTTP/1.1\r\nHost: ws.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(app), nil)
	if err != nil {
		t.Fatalf("读升级响应: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("状态码 = %d，期望 101", resp.StatusCode)
	}
}

// startTLSBannerOrigin 启动一个自签名 TLS 源站：握手后先发 banner、再回显，承载的不是 HTTP。
func startTLSBannerOrigin(t *testing.T, banner string) net.Listener {
	t.Helper()
	cert := selfSignedCert(t, originCN)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.WriteString(conn, banner)
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln
}

// trustOriginFor 让出站 TLS 接受 host 的自签名证书，测试结束还原。
func trustOriginFor(t *testing.T, host string) {
	t.Helper()
	previous := outboundTLSPolicy.Load()
	policy := outboundtls.NewWithRootCAs(nil)
	if _, err := policy.SetInsecureHosts([]string{host}); err != nil {
		t.Fatal(err)
	}
	SetOutboundTLSPolicy(policy)
	t.Cleanup(func() { SetOutboundTLSPolicy(previous) })
}

// TestTransparentTLSNonHTTP：没带 ALPN 的 TLS 连接解密后不是 HTTP 时，明文原样中继到源站，
// 应用照常收发；该主机随后转为盲转发。
func TestTransparentTLSNonHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, banner, payload string
	}{
		{name: "应用先发非 HTTP 字节", payload: "\x10\x0e\x00\x04MQTT"},
		{name: "源站先开口", banner: "* OK ready\r\n", payload: "a1 CAPABILITY\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreScopeGlobals(t)
			resetMITMBypass(t)
			SetDecryptScope(true, "all", nil, nil)
			tunnelUpstream.Store(nil)
			trustOriginFor(t, "mail.test")
			origin := startTLSBannerOrigin(t, tc.banner)

			app, _ := startTransparent(t, origin.Addr())
			conn := tls.Client(app, &tls.Config{ServerName: "mail.test", RootCAs: sniffyRootPool(t)})
			if err := conn.Handshake(); err != nil {
				t.Fatalf("TLS 握手: %v", err)
			}
			expectRead(t, conn, tc.banner)
			if _, err := io.WriteString(conn, tc.payload); err != nil {
				t.Fatal(err)
			}
			expectRead(t, conn, tc.payload)

			if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "mail.test"}); issuer != originCN {
				t.Fatalf("后续连接的签发者 = %q，期望源站 %q", issuer, originCN)
			}

			web := startOrigin(t)
			defer web.Close()
			if issuer := originIssuer(t, web.Addr(), &tls.Config{ServerName: "mail.test"}); issuer == originCN {
				t.Fatal("同一主机另一个端口上的连接不应跟着转为盲转发")
			}
		})
	}
}

// TestTransparentTLSUntrustedOrigin：解密后不是 HTTP、源站证书又不被 sniffy 信任时，这条连接
// 被断开，到该主机的后续连接转为盲转发，由应用自己与源站握手。
func TestTransparentTLSUntrustedOrigin(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	origin := startTLSBannerOrigin(t, "* OK ready\r\n")

	app, done := startTransparent(t, origin.Addr())
	conn := tls.Client(app, &tls.Config{ServerName: "selfsigned.test", RootCAs: sniffyRootPool(t)})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	var verifyErr *tls.CertificateVerificationError
	if err := <-done; !errors.As(err, &verifyErr) {
		t.Fatalf("透明入口返回 %v，期望源站证书校验失败", err)
	}

	if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "selfsigned.test"}); issuer != originCN {
		t.Fatalf("后续连接的签发者 = %q，期望源站 %q", issuer, originCN)
	}
}

// TestTransparentTLSIdleStaysDecryptable：握手后双方都没发数据时无从判断协议，只中继这一条
// 连接，不把主机记入直通名单。
func TestTransparentTLSIdleStaysDecryptable(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	trustOriginFor(t, "idle.test")
	origin := startTLSBannerOrigin(t, "")

	app, _ := startTransparent(t, origin.Addr())
	conn := tls.Client(app, &tls.Config{ServerName: "idle.test", RootCAs: sniffyRootPool(t)})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	time.Sleep(3 * transparentSniffTimeout)
	if _, err := io.WriteString(conn, "late"); err != nil {
		t.Fatal(err)
	}
	expectRead(t, conn, "late")

	if mitmBypass.active(bypassKeyFor("idle.test", origin.Addr())) {
		t.Fatal("无从判断协议的连接不应让主机转为盲转发")
	}
}

// TestTransparentTLSFragmentedRequest：没带 ALPN 的连接上，HTTP 请求分片到达、首片不足以判断
// 时，这条连接照常得到响应，且主机不进直通名单。
func TestTransparentTLSFragmentedRequest(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	trustOriginFor(t, "slow.test")
	origin := startOrigin(t)
	defer origin.Close()

	app, _ := startTransparent(t, origin.Addr())
	conn := tls.Client(app, &tls.Config{ServerName: "slow.test", RootCAs: sniffyRootPool(t)})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	if _, err := io.WriteString(conn, "G"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * transparentSniffTimeout)
	if _, err := io.WriteString(conn, "ET / HTTP/1.1\r\nHost: slow.test\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); string(body) != "TUNNELED" {
		t.Fatalf("响应体 = %q，期望源站的 \"TUNNELED\"", body)
	}
	if mitmBypass.active(bypassKeyFor("slow.test", origin.Addr())) {
		t.Fatal("分片到达的 HTTP 请求不应让主机转为盲转发")
	}
}

// TestConnectTunnelPropagatesHalfClose：CONNECT 盲隧道同样把应用的半关闭传给源站，并送回
// 源站随后的响应。
func TestConnectTunnelPropagatesHalfClose(t *testing.T) {
	restoreScopeGlobals(t)
	SetDecryptScope(false, "all", nil, nil)
	tunnelUpstream.Store(nil)
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		conn, err := origin.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		received, _ := io.ReadAll(conn)
		_, _ = io.WriteString(conn, "ACK "+string(received))
	}()
	proxy := startProxy(t)
	defer proxy.Close()

	conn, reader, err := dialBenchTunnel(proxy.Addr().String(), origin.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "payload"); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	if string(response) != "ACK payload" {
		t.Fatalf("响应 = %q，期望 \"ACK payload\"", response)
	}
}

// tcpPair 返回一对互连的 TCP 连接。
func tcpPair(t *testing.T) (dialed, accepted *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		peer.Close()
	})
	return conn.(*net.TCPConn), peer.(*net.TCPConn)
}

// TestRelayHalfClosedIdle：应用关掉写端之后，源站持续没有数据到点即断开整条连接；源站的
// 响应还在陆续到达时，总时长超过期限也照常送完。
func TestRelayHalfClosedIdle(t *testing.T) {
	const idle = 100 * time.Millisecond

	startRelay := func(t *testing.T) (app, server *net.TCPConn, done <-chan struct{}) {
		app, client := tcpPair(t)
		origin, server := tcpPair(t)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			relay(client, client, origin, origin, idle)
		}()
		_ = app.SetDeadline(time.Now().Add(10 * time.Second))
		_ = server.SetDeadline(time.Now().Add(10 * time.Second))
		return app, server, finished
	}

	t.Run("源站沉默", func(t *testing.T) {
		app, _, done := startRelay(t)
		if err := app.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("源站一直没有数据，中继应在期限到点后结束")
		}
	})

	t.Run("响应陆续到达", func(t *testing.T) {
		app, server, done := startRelay(t)
		const chunks = 8
		go func() {
			defer server.Close()
			// 读到应用关写端才开始应答，每块间隔短于期限、总时长远超期限。
			_, _ = io.ReadAll(server)
			for range chunks {
				time.Sleep(idle / 2)
				_, _ = io.WriteString(server, "x")
			}
		}()
		if err := app.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		response, err := io.ReadAll(app)
		if err != nil {
			t.Fatalf("读响应: %v", err)
		}
		if want := strings.Repeat("x", chunks); string(response) != want {
			t.Fatalf("响应 = %q，期望 %q", response, want)
		}
		<-done
	})
}

// TestTransparentTLSClientCertRequired：TLS 1.3 的源站索要客户端证书时，sniffy 与它的握手照常
// 返回成功，拒绝要到首次读取才收到。这条连接被断开，到该主机的后续连接转为盲转发。
func TestTransparentTLSClientCertRequired(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	trustOriginFor(t, "mtls.test")
	origin, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{selfSignedCert(t, originCN)},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		for {
			conn, err := origin.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.WriteString(conn, "* OK ready\r\n")
			}()
		}
	}()

	app, _ := startTransparent(t, origin.Addr())
	conn := tls.Client(app, &tls.Config{ServerName: "mtls.test", RootCAs: sniffyRootPool(t)})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	if n, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatalf("读到 %d 字节，期望连接被断开", n)
	}

	if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "mtls.test"}); issuer != originCN {
		t.Fatalf("后续连接的签发者 = %q，期望源站 %q", issuer, originCN)
	}
}

// TestTransparentViaProxyKeepsEarlyBytes：经上游代理盲转发时，代理把服务端的 banner 与 CONNECT
// 应答放在同一批发来，banner 照常到达应用。
func TestTransparentViaProxyKeepsEarlyBytes(t *testing.T) {
	restoreScopeGlobals(t)
	const banner = "220 smtp ready\r\n"
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go func() {
		conn, err := proxy.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"+banner)
		_, _ = io.Copy(io.Discard, conn)
	}()
	previous := UpstreamProxyURL()
	SetUpstreamProxyURL(&url.URL{Scheme: "http", Host: proxy.Addr().String()})
	t.Cleanup(func() { SetUpstreamProxyURL(previous) })

	app, _ := startTransparent(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 9), Port: 25})
	expectRead(t, app, banner)
}

// startTLSProxyOrigin 启动一个 HTTPS 代理：对任何请求都回 407。
func startTLSProxyOrigin(t *testing.T) net.Listener {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t, originCN)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
					return
				}
				_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic\r\nContent-Length: 0\r\n\r\n")
			}()
		}
	}()
	return ln
}

// TestTransparentTLSProxyRequest：原始目标是应用自己配置的 HTTPS 代理时，发给它的请求原样到达
// 那台代理、由它应答，sniffy 不按代理语义替它转发；该主机随后转为盲转发。
func TestTransparentTLSProxyRequest(t *testing.T) {
	setup := func(t *testing.T) net.Listener {
		t.Helper()
		restoreScopeGlobals(t)
		resetMITMBypass(t)
		SetDecryptScope(true, "all", nil, nil)
		tunnelUpstream.Store(nil)
		trustOriginFor(t, "proxy.test")
		return startTLSProxyOrigin(t)
	}
	expectBlindForward := func(t *testing.T, proxy net.Listener) {
		t.Helper()
		if issuer := originIssuer(t, proxy.Addr(), &tls.Config{ServerName: "proxy.test"}); issuer != originCN {
			t.Fatalf("后续连接的签发者 = %q，期望代理 %q", issuer, originCN)
		}
	}

	for _, tc := range []struct {
		name, request string
	}{
		{name: "CONNECT", request: "CONNECT target.test:443 HTTP/1.1\r\nHost: target.test:443\r\n\r\n"},
		{name: "absolute-form", request: "GET http://target.test/ HTTP/1.1\r\nHost: target.test\r\n\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := setup(t)
			app, _ := startTransparent(t, proxy.Addr())
			conn := tls.Client(app, &tls.Config{ServerName: "proxy.test", RootCAs: sniffyRootPool(t), NextProtos: []string{"http/1.1"}})
			if err := conn.Handshake(); err != nil {
				t.Fatalf("TLS 握手: %v", err)
			}
			if _, err := io.WriteString(conn, tc.request); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("读应答: %v", err)
			}
			if resp.StatusCode != http.StatusProxyAuthRequired {
				t.Fatalf("状态码 = %d，期望代理的 407", resp.StatusCode)
			}
			expectBlindForward(t, proxy)
		})
	}

	// h2 连接没法中途改成原样中继：这一条被断开，应用重连后即为盲转发。
	t.Run("HTTP/2 上的 CONNECT", func(t *testing.T) {
		proxy := setup(t)
		app, _ := startTransparent(t, proxy.Addr())
		conn := tls.Client(app, &tls.Config{ServerName: "proxy.test", RootCAs: sniffyRootPool(t), NextProtos: []string{"h2"}})
		if err := conn.Handshake(); err != nil {
			t.Fatalf("TLS 握手: %v", err)
		}
		clientConn, err := (&http2.Transport{}).NewClientConn(conn)
		if err != nil {
			t.Fatalf("建立 h2 连接: %v", err)
		}
		body, _ := io.Pipe()
		request, _ := http.NewRequest(http.MethodConnect, "https://target.test:443", body)
		if resp, err := clientConn.RoundTrip(request); err == nil {
			t.Fatalf("CONNECT 得到状态码 %d，期望连接被断开", resp.StatusCode)
		}
		expectBlindForward(t, proxy)
	})

	// 经 h2 的 HTTPS 代理访问 http:// 网址时发的是 :scheme 为 http 的普通请求。
	t.Run("HTTP/2 上的 http 请求", func(t *testing.T) {
		proxy := setup(t)
		app, _ := startTransparent(t, proxy.Addr())
		conn := tls.Client(app, &tls.Config{ServerName: "proxy.test", RootCAs: sniffyRootPool(t), NextProtos: []string{"h2"}})
		if err := conn.Handshake(); err != nil {
			t.Fatalf("TLS 握手: %v", err)
		}
		clientConn, err := (&http2.Transport{}).NewClientConn(conn)
		if err != nil {
			t.Fatalf("建立 h2 连接: %v", err)
		}
		request, _ := http.NewRequest(http.MethodGet, "http://target.test/", nil)
		if resp, err := clientConn.RoundTrip(request); err == nil {
			t.Fatalf("GET 得到状态码 %d，期望连接被断开", resp.StatusCode)
		}
		expectBlindForward(t, proxy)
	})
}

// statusOver 在已握手的连接上按协商出的协议发一个 GET，返回状态码。
func statusOver(t *testing.T, conn *tls.Conn, host string) int {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	roundTrip := func(request *http.Request) (*http.Response, error) {
		if err := request.Write(conn); err != nil {
			return nil, err
		}
		return http.ReadResponse(bufio.NewReader(conn), request)
	}
	if conn.ConnectionState().NegotiatedProtocol == "h2" {
		clientConn, err := (&http2.Transport{}).NewClientConn(conn)
		if err != nil {
			t.Fatalf("建立 h2 连接: %v", err)
		}
		roundTrip = clientConn.RoundTrip
	}
	resp, err := roundTrip(request)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// startClientCertOrigin 启动一个要求客户端证书的自签名 HTTPS 源站，只用 version 这一个 TLS
// 版本，对出示了证书的请求回 "AUTHED"。
func startClientCertOrigin(t *testing.T, version uint16) net.Addr {
	t.Helper()
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "AUTHED")
	}))
	origin.TLS = &tls.Config{
		Certificates: []tls.Certificate{selfSignedCert(t, originCN)},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   version,
		MaxVersion:   version,
	}
	origin.StartTLS()
	t.Cleanup(origin.Close)
	return origin.Listener.Addr()
}

// expectClientCertReconnect 等 done 所属的透明连接断开，再以应用的身份重连 dst 并出示 cert：
// 期望盲转发到 startClientCertOrigin 的源站。
func expectClientCertReconnect(t *testing.T, done <-chan error, dst net.Addr, cert tls.Certificate) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("期望 502 之后连接被断开")
	}
	app, _ := startTransparent(t, dst)
	conn := tls.Client(app, &tls.Config{
		ServerName:         "mtls.test",
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{cert},
	})
	if body := getOver(t, conn, bypassKeyFor("mtls.test", dst)); body != "AUTHED" {
		t.Fatalf("响应体 = %q，期望源站的 \"AUTHED\"", body)
	}
}

// TestTransparentHTTPSClientCertRequired：HTTPS 源站要求客户端证书时，sniffy 代发的请求被它以
// TLS alert 拒绝。这次请求得到 502、连接随后断开，应用重连后即为盲转发，由它自己出示证书。
func TestTransparentHTTPSClientCertRequired(t *testing.T) {
	clientCert := selfSignedCert(t, "client")
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		for _, alpn := range []string{"http/1.1", "h2"} {
			t.Run(tls.VersionName(version)+"/"+alpn, func(t *testing.T) {
				restoreScopeGlobals(t)
				resetMITMBypass(t)
				SetDecryptScope(true, "all", nil, nil)
				tunnelUpstream.Store(nil)
				withPinnedUpstream(t)
				dst := startClientCertOrigin(t, version)
				host := bypassKeyFor("mtls.test", dst)

				app, done := startTransparent(t, dst)
				conn := tls.Client(app, &tls.Config{ServerName: "mtls.test", RootCAs: sniffyRootPool(t), NextProtos: []string{alpn}})
				if err := conn.Handshake(); err != nil {
					t.Fatalf("TLS 握手: %v", err)
				}
				if status := statusOver(t, conn, host); status != http.StatusBadGateway {
					t.Fatalf("状态码 = %d，期望 502", status)
				}
				expectClientCertReconnect(t, done, dst, clientCert)
			})
		}
	}
}

// TestTransparentWSSClientCertRequired：WebSocket 升级请求的上游连接同样由 sniffy 代为握手，
// 源站要求客户端证书时升级得到 502、连接断开，应用重连后即为盲转发。
func TestTransparentWSSClientCertRequired(t *testing.T) {
	clientCert := selfSignedCert(t, "client")
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(tls.VersionName(version), func(t *testing.T) {
			restoreScopeGlobals(t)
			resetMITMBypass(t)
			SetDecryptScope(true, "all", nil, nil)
			tunnelUpstream.Store(nil)
			SetDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, types.PinnedDialAddr(ctx, address))
			})
			t.Cleanup(func() { SetDialContext(nil) })
			trustOriginFor(t, "mtls.test")
			dst := startClientCertOrigin(t, version)
			host := bypassKeyFor("mtls.test", dst)

			app, done := startTransparent(t, dst)
			conn := tls.Client(app, &tls.Config{ServerName: "mtls.test", RootCAs: sniffyRootPool(t), NextProtos: []string{"http/1.1"}})
			if err := conn.Handshake(); err != nil {
				t.Fatalf("TLS 握手: %v", err)
			}
			if _, err := fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", host); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("读升级响应: %v", err)
			}
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("状态码 = %d，期望 502", resp.StatusCode)
			}
			expectClientCertReconnect(t, done, dst, clientCert)
		})
	}
}

// TestNoteOriginRejection：只有透明连接上的 HTTPS 请求被 TLS alert 拒绝才记入直通名单并断开连接。
func TestNoteOriginRejection(t *testing.T) {
	alert := &url.Error{Op: "Get", Err: &net.OpError{Op: "remote error", Err: errors.New("tls: certificate required")}}
	transparent := types.WithOriginalDst(context.Background(), "Api.test:8443", "203.0.113.9:8443")

	for _, tc := range []struct {
		name   string
		ctx    context.Context
		url    string
		cause  error
		bypass bool
	}{
		{name: "TLS alert", ctx: transparent, url: "https://Api.test:8443/", cause: alert, bypass: true},
		{name: "连接被拒绝", ctx: transparent, url: "https://Api.test:8443/", cause: &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
		{name: "明文 HTTP", ctx: transparent, url: "http://Api.test:8443/", cause: alert},
		{name: "代理模式", ctx: context.Background(), url: "https://Api.test:8443/", cause: alert},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetMITMBypass(t)
			request, _ := http.NewRequestWithContext(tc.ctx, http.MethodGet, tc.url, nil)
			responder := &connResponder{p: &Processor{}}
			noteOriginRejection(newMockServer(), request, tc.cause, responder)
			if got := mitmBypass.active("api.test:8443"); got != tc.bypass {
				t.Fatalf("直通 = %v，期望 %v", got, tc.bypass)
			}
			if got := responder.p.closeAfterResponse; got != tc.bypass {
				t.Fatalf("断开连接 = %v，期望 %v", got, tc.bypass)
			}
		})
	}
}
