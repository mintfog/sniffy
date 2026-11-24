// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
	"golang.org/x/net/http2"
)

// handshakeThenClose 经透明入口与 dst 完成一次信任 sniffy 根证书的握手，等 wait 后不发任何
// 应用层字节就关闭连接，并等透明入口处理完这条连接：应用正常关闭不算出错。
func handshakeThenClose(t *testing.T, dst net.Addr, host string, alpn []string, wait time.Duration) {
	t.Helper()
	app, done := startTransparent(t, dst)
	conn := tls.Client(app, &tls.Config{ServerName: host, RootCAs: sniffyRootPool(t), NextProtos: alpn})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	time.Sleep(wait)
	_ = conn.Close()
	if err := <-done; err != nil {
		t.Fatalf("透明入口返回 %v，期望应用正常关闭时返回 nil", err)
	}
}

// TestTransparentTLSClosedAfterHandshake：握手成功后一个字节不发就关闭连接（握手后才校验证书
// 的程序拒绝证书时的表现），连续两次后主机转为盲转发，原因记为握手后断开。
func TestTransparentTLSClosedAfterHandshake(t *testing.T) {
	for _, tc := range []struct {
		name string
		alpn []string
	}{
		{"h2", []string{"h2", "http/1.1"}},
		{"http/1.1", []string{"http/1.1"}},
		{"不带 ALPN", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreScopeGlobals(t)
			resetMITMBypass(t)
			SetDecryptScope(true, "all", nil, nil)
			tunnelUpstream.Store(nil)
			origin := startOrigin(t)
			defer origin.Close()
			key := bypassKeyFor("pinned.test", origin.Addr())

			handshakeThenClose(t, origin.Addr(), "pinned.test", tc.alpn, 0)
			if mitmBypass.active(key) {
				t.Fatal("一次握手后断开不应转为盲转发")
			}
			handshakeThenClose(t, origin.Addr(), "pinned.test", tc.alpn, 0)
			if mitmBypass.active(key) || len(BypassHosts()) != 0 {
				t.Fatal("第二次握手后断开要等 mitmBypassSettle 才转为盲转发")
			}
			passBypassSettle(t)
			if !mitmBypass.active(key) {
				t.Fatal("连续两次握手后断开、等过 mitmBypassSettle 后应转为盲转发")
			}
			if got := mitmBypass.hosts[key].reason; got != bypassClosedAfterHandshake {
				t.Fatalf("原因 = %q，期望 %q", got, bypassClosedAfterHandshake)
			}
			if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "pinned.test", NextProtos: tc.alpn}); issuer != originCN {
				t.Fatalf("签发者 = %q，期望源站 %q", issuer, originCN)
			}
		})
	}
}

// TestTransparentTLSSiblingClosesNotCounted：浏览器对同一主机并发建连，选中一条发请求后随即关掉
// 其余几条。被关掉的连接与发出请求的那条只差几毫秒，先后不定，都不让主机转为盲转发。
func TestTransparentTLSSiblingClosesNotCounted(t *testing.T) {
	for _, tc := range []struct {
		name          string
		closeFirst    bool
		alpn          []string
		siblingCloses int
	}{
		{"h2，先关多余连接", true, []string{"h2", "http/1.1"}, 5},
		{"h2，先发请求", false, []string{"h2", "http/1.1"}, 5},
		{"http/1.1，先关多余连接", true, []string{"http/1.1"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreScopeGlobals(t)
			resetMITMBypass(t)
			SetDecryptScope(true, "all", nil, nil)
			tunnelUpstream.Store(nil)
			withPinnedUpstream(t)
			origin := startOrigin(t)
			defer origin.Close()

			pool := sniffyRootPool(t)
			handshake := func() (*tls.Conn, <-chan error) {
				app, done := startTransparent(t, origin.Addr())
				conn := tls.Client(app, &tls.Config{ServerName: "cdn.test", RootCAs: pool, NextProtos: tc.alpn})
				if err := conn.Handshake(); err != nil {
					t.Fatalf("TLS 握手: %v", err)
				}
				return conn, done
			}
			chosen, chosenDone := handshake()
			siblings := make([]*tls.Conn, tc.siblingCloses)
			siblingDones := make([]<-chan error, tc.siblingCloses)
			for i := range siblings {
				siblings[i], siblingDones[i] = handshake()
			}
			closeSiblings := func() {
				for i, conn := range siblings {
					_ = conn.Close()
					<-siblingDones[i]
				}
			}
			request := func() {
				if chosen.ConnectionState().NegotiatedProtocol == "h2" {
					if _, err := io.WriteString(chosen, http2.ClientPreface); err != nil {
						t.Fatal(err)
					}
				} else if body := getOver(t, chosen, "cdn.test"); body != "TUNNELED" {
					t.Fatalf("响应体 = %q，期望源站的 \"TUNNELED\"", body)
				}
				// 等透明入口读到首批字节，成功先于随后的断开记下。
				deadline := time.Now().Add(5 * time.Second)
				for {
					mitmBypass.mu.Lock()
					_, seen := mitmBypass.succeeded[bypassKeyFor("cdn.test", origin.Addr())]
					mitmBypass.mu.Unlock()
					if seen || time.Now().After(deadline) {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			if tc.closeFirst {
				closeSiblings()
				request()
			} else {
				request()
				closeSiblings()
			}
			_ = chosen.Close()
			<-chosenDone

			passBypassSettle(t)
			if mitmBypass.active(bypassKeyFor("cdn.test", origin.Addr())) {
				t.Fatal("与发出请求的连接同时关掉的多余连接不应让主机转为盲转发")
			}
			if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "cdn.test", NextProtos: tc.alpn}); issuer == originCN {
				t.Fatal("主机应继续解密，却拿到了源站证书")
			}
		})
	}
}

// TestTransparentTLSRequestClearsStrike：握手后发出请求的连接不计失败，并清掉此前未生效的
// 失败计数。
func TestTransparentTLSRequestClearsStrike(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	withPinnedUpstream(t)
	origin := startOrigin(t)
	defer origin.Close()
	alpn := []string{"http/1.1"}

	handshakeThenClose(t, origin.Addr(), "app.test", alpn, 0)

	app, done := startTransparent(t, origin.Addr())
	conn := tls.Client(app, &tls.Config{ServerName: "app.test", RootCAs: sniffyRootPool(t), NextProtos: alpn})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	if body := getOver(t, conn, "app.test"); body != "TUNNELED" {
		t.Fatalf("响应体 = %q，期望源站的 \"TUNNELED\"", body)
	}
	_ = conn.Close()
	<-done

	handshakeThenClose(t, origin.Addr(), "app.test", alpn, 0)
	if mitmBypass.active(bypassKeyFor("app.test", origin.Addr())) {
		t.Fatal("两次断开之间发过请求，不应转为盲转发")
	}
}

// TestTransparentTLSLateRequestClearsStrike：握手后空闲超过 postHandshakeWindow 才发请求的
// 连接（浏览器的预连接）同样清掉此前未生效的失败计数。
func TestTransparentTLSLateRequestClearsStrike(t *testing.T) {
	for _, tc := range []struct {
		name string
		alpn []string
		get  func(t *testing.T, conn *tls.Conn) string
	}{
		{"http/1.1", []string{"http/1.1"}, func(t *testing.T, conn *tls.Conn) string {
			return getOver(t, conn, "app.test")
		}},
		{"h2", []string{"h2"}, func(t *testing.T, conn *tls.Conn) string {
			clientConn, err := (&http2.Transport{}).NewClientConn(conn)
			if err != nil {
				t.Fatalf("建立 h2 连接: %v", err)
			}
			request, _ := http.NewRequest(http.MethodGet, "https://app.test/", nil)
			resp, err := clientConn.RoundTrip(request)
			if err != nil {
				t.Fatalf("h2 请求: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			return string(body)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreScopeGlobals(t)
			resetMITMBypass(t)
			SetDecryptScope(true, "all", nil, nil)
			tunnelUpstream.Store(nil)
			withPinnedUpstream(t)
			origin := startOrigin(t)
			defer origin.Close()

			handshakeThenClose(t, origin.Addr(), "app.test", tc.alpn, 0)

			app, done := startTransparent(t, origin.Addr())
			conn := tls.Client(app, &tls.Config{ServerName: "app.test", RootCAs: sniffyRootPool(t), NextProtos: tc.alpn})
			if err := conn.Handshake(); err != nil {
				t.Fatalf("TLS 握手: %v", err)
			}
			time.Sleep(postHandshakeWindow + 200*time.Millisecond)
			if body := tc.get(t, conn); body != "TUNNELED" {
				t.Fatalf("响应体 = %q，期望源站的 \"TUNNELED\"", body)
			}
			_ = conn.Close()
			<-done

			handshakeThenClose(t, origin.Addr(), "app.test", tc.alpn, 0)
			if mitmBypass.active(bypassKeyFor("app.test", origin.Addr())) {
				t.Fatal("两次断开之间有空闲后发出请求的连接，不应转为盲转发")
			}
		})
	}
}

// TestTransparentTLSIdleCloseNotCounted：握手后空闲超过 postHandshakeWindow 才关闭的连接
// （浏览器的预连接）不计失败。
func TestTransparentTLSIdleCloseNotCounted(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	origin := startOrigin(t)
	defer origin.Close()
	alpn := []string{"h2", "http/1.1"}

	for range mitmBypassStrikes {
		handshakeThenClose(t, origin.Addr(), "idle.test", alpn, postHandshakeWindow+200*time.Millisecond)
	}
	if _, ok := mitmBypass.hosts[bypassKeyFor("idle.test", origin.Addr())]; ok {
		t.Fatal("空闲超过时限才关闭的连接不应计入失败")
	}
}

// TestSetCAForgetsCertRejections：换了根证书后清除应用拒绝证书的记录，与根证书无关的保留。
func TestSetCAForgetsCertRejections(t *testing.T) {
	resetMITMBypass(t)
	previous := currentCA()
	t.Cleanup(func() { SetCA(previous) })

	mitmBypass.add("cert.test:443", bypassCertRejected, mitmBypassStrikes)
	mitmBypass.add("pinned.test:443", bypassClosedAfterHandshake, 1)
	kept := map[string]bypassReason{
		"mail.test:993":  bypassNotHTTP,
		"proxy.test:443": bypassHTTPSProxy,
		"mtls.test:443":  bypassOriginRejected,
	}
	for host, reason := range kept {
		mitmBypass.add(host, reason, mitmBypassStrikes)
	}
	regenerated, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	SetCA(regenerated)
	for _, host := range []string{"cert.test:443", "pinned.test:443"} {
		if _, ok := mitmBypass.hosts[host]; ok {
			t.Errorf("%s 的记录应随根证书更换清除", host)
		}
	}
	for host := range kept {
		if !mitmBypass.active(host) {
			t.Errorf("%s 的记录与根证书无关，应保留", host)
		}
	}
}

// TestSetCAIgnoresStaleConnections：换根证书前已握手的连接在换之后才断开，结论针对的是旧根
// 证书，不计入直通名单。
func TestSetCAIgnoresStaleConnections(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	origin := startOrigin(t)
	defer origin.Close()
	previous := currentCA()
	t.Cleanup(func() { SetCA(previous) })

	pool := sniffyRootPool(t)
	conns := make([]*tls.Conn, 0, mitmBypassStrikes)
	dones := make([]<-chan error, 0, mitmBypassStrikes)
	for range mitmBypassStrikes {
		app, done := startTransparent(t, origin.Addr())
		conn := tls.Client(app, &tls.Config{ServerName: "pinned.test", RootCAs: pool, NextProtos: []string{"http/1.1"}})
		if err := conn.Handshake(); err != nil {
			t.Fatalf("TLS 握手: %v", err)
		}
		conns, dones = append(conns, conn), append(dones, done)
	}
	regenerated, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	SetCA(regenerated)
	for i, conn := range conns {
		_ = conn.Close()
		<-dones[i]
	}
	if _, ok := mitmBypass.hosts[bypassKeyFor("pinned.test", origin.Addr())]; ok {
		t.Fatal("旧根证书下握手的连接不应在换根证书后计入失败")
	}
}

// TestTransparentTLSRelayTransportFailure：中继时与源站的握手因传输层失败（源站接受连接后
// 立即关闭）而失败，只让这一条连接失败，主机不进直通名单。
func TestTransparentTLSRelayTransportFailure(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	origin, err := net.Listen("tcp", "127.0.0.1:0")
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
			_ = conn.Close()
		}
	}()

	app, done := startTransparent(t, origin.Addr())
	conn := tls.Client(app, &tls.Config{ServerName: "flaky.test", RootCAs: sniffyRootPool(t)})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	if _, err := io.WriteString(conn, "a001 CAPABILITY\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("期望透明入口报告与源站的握手失败")
	}
	if _, ok := mitmBypass.hosts[bypassKeyFor("flaky.test", origin.Addr())]; ok {
		t.Fatal("传输层失败不应让主机进直通名单")
	}
}

func TestIsTransportFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"超时", &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}, true},
		{"连接被关闭", io.EOF, true},
		{"连接被重置", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, true},
		{"对端 alert", &net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}, false},
		{"本端 alert", &net.OpError{Op: "local error", Err: errors.New("tls: unexpected message")}, false},
		{"证书校验不通过", &tls.CertificateVerificationError{Err: errors.New("x509: unknown authority")}, false},
		{"协商不出共同参数", errors.New("tls: server selected unsupported protocol version 301"), false},
	}
	for _, tc := range cases {
		if got := isTransportFailure(tc.err); got != tc.want {
			t.Errorf("%s：isTransportFailure = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestTransparentCAUntrustedBlindForwards：根证书未被信任时透明连接上的 TLS 一律盲转发，明文 HTTP
// 照常解析。
func TestTransparentCAUntrustedBlindForwards(t *testing.T) {
	restoreScopeGlobals(t)
	resetMITMBypass(t)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)
	withPinnedUpstream(t)
	SetTransparentCATrusted(false)
	t.Cleanup(func() { SetTransparentCATrusted(true) })

	t.Run("TLS", func(t *testing.T) {
		origin := startOrigin(t)
		defer origin.Close()
		for _, alpn := range [][]string{{"h2", "http/1.1"}, nil} {
			if issuer := originIssuer(t, origin.Addr(), &tls.Config{ServerName: "mitm.test", NextProtos: alpn}); issuer != originCN {
				t.Fatalf("ALPN %v：签发者 = %q，期望源站 %q", alpn, issuer, originCN)
			}
		}
	})
	// 不带 Host 的 HTTP/1.0 请求经解析后才会以原始目标补上 Host，源站看到的 Host 能区分
	// 解析与盲转发。
	t.Run("明文 HTTP", func(t *testing.T) {
		originLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "origin saw "+r.Host)
		})}
		go func() { _ = origin.Serve(originLn) }()
		defer origin.Close()

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
		if want := "origin saw " + originLn.Addr().String(); string(body) != want {
			t.Fatalf("响应体 = %q，期望 %q", body, want)
		}
	})
}

// TestBypassHostsListAndForget：名单只列出已生效的记录、带上原因与生效时间；删掉一条后到它的
// 透明连接重新尝试解密，未生效的计数一并删掉；清空后全部重新尝试。
func TestBypassHostsListAndForget(t *testing.T) {
	resetMITMBypass(t)
	before := time.Now()
	mitmBypass.add("pinned.test:443", bypassCertRejected, mitmBypassStrikes)
	mitmBypass.add("mail.test:993", bypassNotHTTP, mitmBypassStrikes)
	mitmBypass.add("flaky.test:443", bypassClosedAfterHandshake, 1)

	list := BypassHosts()
	if len(list) != 2 || list[0].Host != "mail.test:993" || list[1].Host != "pinned.test:443" {
		t.Fatalf("直通名单 = %+v，期望按主机排序、不含未生效的计数", list)
	}
	if list[0].Reason != "not_http" || list[1].Reason != "cert_rejected" {
		t.Errorf("原因 = %q、%q", list[0].Reason, list[1].Reason)
	}
	if list[1].Since.Before(before) || list[1].Since.After(time.Now()) {
		t.Errorf("生效时间 = %v", list[1].Since)
	}

	ForgetBypass("PINNED.test:443")
	ForgetBypass("flaky.test:443")
	if mitmBypass.active("pinned.test:443") {
		t.Error("删掉的主机仍在盲转发")
	}
	if _, ok := mitmBypass.hosts["flaky.test:443"]; ok {
		t.Error("未生效的计数应一并删掉")
	}
	if !mitmBypass.active("mail.test:993") {
		t.Error("没删的记录不应受影响")
	}

	ClearBypass()
	if got := BypassHosts(); len(got) != 0 {
		t.Errorf("清空后名单 = %+v", got)
	}
}

// TestSetTransparentCATrustedForgetsOnTrust：只有由未信任变为信任时才清除应用拒绝证书的记录，
// 其余原因的记录保留。
func TestSetTransparentCATrustedForgetsOnTrust(t *testing.T) {
	resetMITMBypass(t)
	t.Cleanup(func() { SetTransparentCATrusted(true) })
	seed := func() {
		mitmBypass.add("pinned.test:443", bypassCertRejected, mitmBypassStrikes)
		mitmBypass.add("mail.test:993", bypassNotHTTP, mitmBypassStrikes)
	}
	for _, step := range []struct {
		trusted bool
		kept    bool
	}{
		{trusted: true, kept: true},  // 一直信任
		{trusted: false, kept: true}, // 变为未信任
		{trusted: false, kept: true}, // 一直未信任
		{trusted: true, kept: false}, // 由未信任变为信任
		{trusted: true, kept: true},  // 再次确认信任
	} {
		seed()
		SetTransparentCATrusted(step.trusted)
		if got := mitmBypass.active("pinned.test:443"); got != step.kept {
			t.Errorf("trusted=%v 后拒绝证书的记录还在 = %v，期望 %v", step.trusted, got, step.kept)
		}
		if !mitmBypass.active("mail.test:993") {
			t.Errorf("trusted=%v 后与根证书无关的记录被清除了", step.trusted)
		}
	}
}
