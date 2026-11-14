// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// transparentTestConn 给 net.Pipe 的一端套上透明连接的地址约定。
type transparentTestConn struct {
	net.Conn
	local, remote net.Addr
}

func (c *transparentTestConn) LocalAddr() net.Addr  { return c.local }
func (c *transparentTestConn) RemoteAddr() net.Addr { return c.remote }

// dialTransparent 把一条原始目标为 dst 的透明连接交给引擎，返回应用一侧的连接。
func dialTransparent(t *testing.T, engine *Engine, dst net.Addr) net.Conn {
	t.Helper()
	proxySide, appSide := net.Pipe()
	t.Cleanup(func() { appSide.Close() })
	_ = appSide.SetDeadline(time.Now().Add(10 * time.Second))
	go engine.Listener().ServeTransparent(&transparentTestConn{
		Conn:   proxySide,
		local:  dst,
		remote: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 50000},
	})
	return appSide
}

func transparentGet(t *testing.T, conn net.Conn, host string) string {
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

// TestEngineTransparentDialsOriginalDst：透明连接上的请求经引擎自己的出站链路连到应用的原始
// 目标。域名 pinned.invalid 无法解析，只有按原始目标拨号才到得了源站。
func TestEngineTransparentDialsOriginalDst(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "origin saw "+r.Host)
	})

	t.Run("明文 HTTP", func(t *testing.T) {
		origin := httptest.NewServer(handler)
		defer origin.Close()
		engine := newProbeEngine(t)
		startProbeEngine(t, engine)

		app := dialTransparent(t, engine, origin.Listener.Addr())
		if body := transparentGet(t, app, "pinned.invalid"); body != "origin saw pinned.invalid" {
			t.Fatalf("响应体 = %q", body)
		}
	})

	t.Run("HTTPS", func(t *testing.T) {
		origin := httptest.NewTLSServer(handler)
		defer origin.Close()
		engine := newProbeEngine(t)
		// 源站用的是 httptest 的自签证书。
		if err := engine.SetTLSInsecureHosts([]string{"pinned.invalid"}); err != nil {
			t.Fatalf("SetTLSInsecureHosts: %v", err)
		}
		startProbeEngine(t, engine)

		app := tls.Client(dialTransparent(t, engine, origin.Listener.Addr()),
			&tls.Config{ServerName: "pinned.invalid", InsecureSkipVerify: true})
		if err := app.Handshake(); err != nil {
			t.Fatalf("TLS 握手: %v", err)
		}
		if body := transparentGet(t, app, "pinned.invalid"); body != "origin saw pinned.invalid" {
			t.Fatalf("响应体 = %q", body)
		}
	})

	// 配置了上游代理时域名交给代理解析，不拨原始目标。
	t.Run("经上游代理", func(t *testing.T) {
		origin := httptest.NewServer(handler)
		defer origin.Close()
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "proxy saw "+r.URL.String())
		}))
		defer proxy.Close()
		engine := newProbeEngine(t)
		if err := engine.SetUpstreamProxy(proxy.URL); err != nil {
			t.Fatalf("SetUpstreamProxy: %v", err)
		}
		t.Cleanup(func() { _ = engine.SetUpstreamProxy("") })
		startProbeEngine(t, engine)

		app := dialTransparent(t, engine, origin.Listener.Addr())
		if body := transparentGet(t, app, "pinned.invalid"); body != "proxy saw http://pinned.invalid/" {
			t.Fatalf("响应体 = %q", body)
		}
	})

	// 请求的 Host 与上游代理地址相同时，连的仍是代理：带着代理凭据的请求只发给代理。
	t.Run("Host 与上游代理地址相同", func(t *testing.T) {
		var originHits atomic.Int32
		origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			originHits.Add(1)
		}))
		defer origin.Close()
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "proxy saw "+r.Header.Get("Proxy-Authorization"))
		}))
		defer proxy.Close()
		proxyAddr := proxy.Listener.Addr().String()
		engine := newProbeEngine(t)
		if err := engine.SetUpstreamProxy("http://user:secret@" + proxyAddr); err != nil {
			t.Fatalf("SetUpstreamProxy: %v", err)
		}
		t.Cleanup(func() { _ = engine.SetUpstreamProxy("") })
		startProbeEngine(t, engine)

		app := dialTransparent(t, engine, origin.Listener.Addr())
		want := "proxy saw Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
		if body := transparentGet(t, app, proxyAddr); body != want {
			t.Fatalf("响应体 = %q，期望 %q", body, want)
		}
		if n := originHits.Load(); n != 0 {
			t.Fatalf("原始目标收到 %d 个请求，期望 0", n)
		}
	})
}

// namedOrigin 返回一个只回自己名字的源站处理器。
func namedOrigin(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, name)
	})
}

// TestEngineTransparentIsolatesPoolsByOriginalDst：同一域名先后连向两个原始目标时，后一个
// 请求不能复用前一个目标留在连接池里的连接。
func TestEngineTransparentIsolatesPoolsByOriginalDst(t *testing.T) {
	const host = "pinned.invalid"

	// 入站 HTTP/1.x 走保真转发的连接池。请求不带 Connection: close，出站连接才会留在池里。
	t.Run("HTTP/1.1", func(t *testing.T) {
		originA, originB := httptest.NewServer(namedOrigin("A")), httptest.NewServer(namedOrigin("B"))
		defer originA.Close()
		defer originB.Close()
		engine := newProbeEngine(t)
		startProbeEngine(t, engine)

		for _, tc := range []struct {
			origin *httptest.Server
			want   string
		}{{originA, "A"}, {originB, "B"}, {originA, "A"}} {
			app := dialTransparent(t, engine, tc.origin.Listener.Addr())
			if _, err := fmt.Fprintf(app, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", host); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(app), nil)
			if err != nil {
				t.Fatalf("读响应: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(body) != tc.want {
				t.Fatalf("请求到了源站 %q，期望 %q", body, tc.want)
			}
		}
	})

	// 入站 HTTP/2 走标准 Transport，出站同样协商为 HTTP/2。
	t.Run("HTTP/2", func(t *testing.T) {
		startH2Origin := func(name string) *httptest.Server {
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, name+" "+r.Proto)
			}))
			origin.EnableHTTP2 = true
			origin.StartTLS()
			return origin
		}
		originA, originB := startH2Origin("A"), startH2Origin("B")
		defer originA.Close()
		defer originB.Close()
		engine := newProbeEngine(t)
		if err := engine.SetTLSInsecureHosts([]string{host}); err != nil {
			t.Fatalf("SetTLSInsecureHosts: %v", err)
		}
		startProbeEngine(t, engine)

		for _, tc := range []struct {
			origin *httptest.Server
			want   string
		}{{originA, "A HTTP/2.0"}, {originB, "B HTTP/2.0"}, {originA, "A HTTP/2.0"}} {
			app := tls.Client(dialTransparent(t, engine, tc.origin.Listener.Addr()),
				&tls.Config{ServerName: host, InsecureSkipVerify: true, NextProtos: []string{"h2"}})
			if err := app.Handshake(); err != nil {
				t.Fatalf("TLS 握手: %v", err)
			}
			clientConn, err := (&http2.Transport{}).NewClientConn(app)
			if err != nil {
				t.Fatalf("建立 h2 连接: %v", err)
			}
			request, _ := http.NewRequest(http.MethodGet, "https://"+host+"/", nil)
			resp, err := clientConn.RoundTrip(request)
			if err != nil {
				t.Fatalf("h2 请求: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(body) != tc.want {
				t.Fatalf("请求到了源站 %q，期望 %q", body, tc.want)
			}
		}
	})
}
