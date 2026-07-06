// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package forward

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// --- 纯单元测试:无网络,直接打到各辅助函数 ---

func TestBasicAuth(t *testing.T) {
	// want 值直接写死 base64,防止用同一构造式与被测函数自证同义反复,
	// 也顺便固定参数顺序(空口令 / 空用户名两条挑出反参数的实现)。
	cases := []struct {
		name, user, pass, want string
	}{
		{"user and pass", "user", "pass", "dXNlcjpwYXNz"},
		{"empty pass", "u", "", "dTo="},
		{"empty user", "", "p", "OnA="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := basicAuth(c.user, c.pass); got != c.want {
				t.Fatalf("basicAuth(%q,%q) = %q, want %q", c.user, c.pass, got, c.want)
			}
		})
	}
}

func TestCanonicalAddr(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://h.example/p", "h.example:80"},
		{"https://h.example/p", "h.example:443"},
		{"http://h.example:8080/p", "h.example:8080"},
		{"https://[::1]/p", "[::1]:443"},
		{"https://[::1]:8443/p", "[::1]:8443"},
	}
	for _, c := range cases {
		if got := canonicalAddr(mustURL(t, c.in)); got != c.want {
			t.Errorf("canonicalAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestConnKey(t *testing.T) {
	u := mustURL(t, "http://h.example/p")
	if got, want := connKey(u, nil), "http\x00h.example:80\x00"; got != want {
		t.Errorf("connKey 无代理 = %q, want %q", got, want)
	}
	proxy := mustURL(t, "http://127.0.0.1:8080")
	want := "http\x00h.example:80\x00" + proxy.String()
	if got := connKey(u, proxy); got != want {
		t.Errorf("connKey 带代理 = %q, want %q", got, want)
	}
}

func TestTokenListContains(t *testing.T) {
	cases := []struct {
		v, token string
		want     bool
	}{
		{"gzip, upgrade", "upgrade", true},
		{"a, B ,c", "b", true}, // 大小写不敏感 + 去空格
		{"gzip", "upgrade", false},
		{"", "upgrade", false},
	}
	for _, c := range cases {
		if got := tokenListContains(c.v, c.token); got != c.want {
			t.Errorf("tokenListContains(%q,%q) = %v, want %v", c.v, c.token, got, c.want)
		}
	}
}

func TestHeaderBlockEnd(t *testing.T) {
	crlf := []byte("HTTP/1.1 200 OK\r\nA: b\r\n\r\nbody")
	if got := headerBlockEnd(crlf); got != len("HTTP/1.1 200 OK\r\nA: b\r\n") {
		t.Errorf("CRLFCRLF 结束位置错误: %d", got)
	}
	lf := []byte("HTTP/1.1 200 OK\nA: b\n\nbody")
	if got := headerBlockEnd(lf); got != len("HTTP/1.1 200 OK\nA: b\n") {
		t.Errorf("LF-only 结束位置错误: %d", got)
	}
	if got := headerBlockEnd([]byte("HTTP/1.1 200 OK\r\nA: b\r\n")); got != -1 {
		t.Errorf("未结束应返回 -1, 实得 %d", got)
	}
}

func TestParseResponseHead(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		status, hdr := parseResponseHead([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nX-A: 1\r\n"))
		if status != "HTTP/1.1 200 OK" {
			t.Fatalf("status = %q", status)
		}
		want := [][2]string{{"Content-Type", "text/html"}, {"X-A", "1"}}
		if !reflect.DeepEqual(hdr, want) {
			t.Fatalf("hdr = %v, want %v", hdr, want)
		}
	})

	t.Run("obs-fold continuation", func(t *testing.T) {
		_, hdr := parseResponseHead([]byte("HTTP/1.1 200 OK\r\nX-Long: a\r\n b\r\n"))
		want := [][2]string{{"X-Long", "a b"}}
		if !reflect.DeepEqual(hdr, want) {
			t.Fatalf("obs-fold 续行未并入: %v", hdr)
		}
	})

	t.Run("line without colon skipped", func(t *testing.T) {
		_, hdr := parseResponseHead([]byte("HTTP/1.1 200 OK\r\nGarbageNoColon\r\nX: y\r\n"))
		want := [][2]string{{"X", "y"}}
		if !reflect.DeepEqual(hdr, want) {
			t.Fatalf("无冒号行应被跳过: %v", hdr)
		}
	})

	t.Run("leading fold without prior header", func(t *testing.T) {
		// 续行出现在没有任何头之前,应被安全忽略(不 panic、不入列)。
		_, hdr := parseResponseHead([]byte("HTTP/1.1 200 OK\r\n  orphanfold\r\nX: y\r\n"))
		want := [][2]string{{"X", "y"}}
		if !reflect.DeepEqual(hdr, want) {
			t.Fatalf("孤立续行处理错误: %v", hdr)
		}
	})
}

func TestNormalizeFraming(t *testing.T) {
	t.Run("consistent passthrough", func(t *testing.T) {
		in := [][2]string{{"Host", "h"}, {"Content-Length", "3"}}
		got := normalizeFraming(in, 3)
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("自洽应原样返回: %v", got)
		}
	})

	t.Run("no body no CL passthrough", func(t *testing.T) {
		in := [][2]string{{"Host", "h"}, {"Accept", "*/*"}}
		got := normalizeFraming(in, 0)
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("无体无 CL 应原样返回: %v", got)
		}
	})

	t.Run("duplicate CL rebuilt", func(t *testing.T) {
		in := [][2]string{{"Content-Length", "3"}, {"Content-Length", "4"}}
		got := normalizeFraming(in, 3)
		want := [][2]string{{"Content-Length", "3"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("重复 CL 应被重建为单个正确值: %v", got)
		}
	})

	t.Run("transfer-encoding stripped and rebuilt", func(t *testing.T) {
		in := [][2]string{{"transfer-encoding", "chunked"}, {"X", "y"}}
		got := normalizeFraming(in, 5)
		want := [][2]string{{"X", "y"}, {"Content-Length", "5"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("TE 应被剔除并据 body 补 CL: %v", got)
		}
	})

	t.Run("CL mismatch rebuilt", func(t *testing.T) {
		in := [][2]string{{"Content-Length", "99"}}
		got := normalizeFraming(in, 3)
		want := [][2]string{{"Content-Length", "3"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("CL 与 body 不符应被纠正: %v", got)
		}
	})

	t.Run("CL with empty body dropped", func(t *testing.T) {
		in := [][2]string{{"Content-Length", "5"}}
		got := normalizeFraming(in, 0)
		if len(got) != 0 {
			t.Fatalf("空 body 下错误 CL 应被剔除且不补新 CL: %v", got)
		}
	})
}

func TestResolveProxy(t *testing.T) {
	t.Run("nil proxy", func(t *testing.T) {
		tr := New(Config{Fallback: &errRT{}})
		u, err := tr.ResolveProxy(&http.Request{})
		if err != nil || u != nil {
			t.Fatalf("无代理应返回 nil,nil, 实得 %v,%v", u, err)
		}
	})
	t.Run("with proxy", func(t *testing.T) {
		want := mustURL(t, "http://127.0.0.1:8080")
		tr := New(Config{Fallback: &errRT{}, Proxy: func(*http.Request) (*url.URL, error) { return want, nil }})
		u, err := tr.ResolveProxy(&http.Request{})
		if err != nil || u != want {
			t.Fatalf("应返回配置的代理, 实得 %v,%v", u, err)
		}
	})
	t.Run("proxy error propagated", func(t *testing.T) {
		errFake := errors.New("fake proxy resolve error")
		tr := New(Config{Fallback: &errRT{}, Proxy: func(*http.Request) (*url.URL, error) { return nil, errFake }})
		u, err := tr.ResolveProxy(&http.Request{})
		if u != nil || err != errFake {
			t.Fatalf("代理错误应原样透传, 实得 u=%v, err=%v", u, err)
		}
	})
}

// closableFallback 是带 CloseIdleConnections 的回退器,验证委托关闭。
type closableFallback struct{ closed int32 }

func (c *closableFallback) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("not used")
}
func (c *closableFallback) CloseIdleConnections() { atomic.AddInt32(&c.closed, 1) }

func TestCloseIdleConnections(t *testing.T) {
	fb := &closableFallback{}
	tr := New(Config{Fallback: fb})
	tr.idle["k"] = []*persistConn{{key: "k"}, {key: "k"}}
	tr.CloseIdleConnections()
	tr.mu.Lock()
	n := len(tr.idle)
	tr.mu.Unlock()
	if n != 0 {
		t.Fatalf("空闲池应被清空, 实得 %d 个键", n)
	}
	if atomic.LoadInt32(&fb.closed) != 1 {
		t.Fatalf("应委托回退器的 CloseIdleConnections, 实得 %d", fb.closed)
	}
}

func TestGetIdleEvictsExpired(t *testing.T) {
	tr := New(Config{Fallback: &errRT{}, IdleConnTimeout: time.Millisecond})
	expired := &persistConn{key: "k", idleAt: time.Now().Add(-time.Hour)}
	tr.idle["k"] = []*persistConn{expired}
	if got := tr.getIdle("k"); got != nil {
		t.Fatalf("过期空闲连接应被丢弃, 实得 %v", got)
	}
	tr.mu.Lock()
	_, exists := tr.idle["k"]
	tr.mu.Unlock()
	if exists {
		t.Fatalf("清空后该键应被删除")
	}
}

func TestPutIdleDropsBroken(t *testing.T) {
	tr := New(Config{Fallback: &errRT{}})
	pc := &persistConn{key: "k"}
	pc.broken.Store(true)
	tr.putIdle(pc)
	tr.mu.Lock()
	n := len(tr.idle["k"])
	tr.mu.Unlock()
	if n != 0 {
		t.Fatalf("broken 连接不应入池, 实得 %d", n)
	}
}

func TestPutIdleEvictsOldestOnOverflow(t *testing.T) {
	tr := New(Config{Fallback: &errRT{}, MaxIdlePerHost: 1})
	pc1 := &persistConn{key: "k"}
	pc2 := &persistConn{key: "k"}
	tr.putIdle(pc1)
	tr.putIdle(pc2)
	tr.mu.Lock()
	list := tr.idle["k"]
	tr.mu.Unlock()
	if len(list) != 1 || list[0] != pc2 {
		t.Fatalf("超上限应淘汰最旧、保留最新, 实得 %v", list)
	}
}

func TestPersistConnCloseNilSafe(t *testing.T) {
	var pc *persistConn
	pc.close() // nil 接收者不应 panic
}

func TestPooledBodyCloseIdempotent(t *testing.T) {
	tr := New(Config{Fallback: &errRT{}})
	pc := &persistConn{key: "k"}
	guard := newConnGuard(context.Background(), pc) // 不可取消 ctx → 空守护
	b := &pooledBody{rc: io.NopCloser(strings.NewReader("")), pc: pc, t: tr, guard: guard, reusable: false}
	if err := b.Close(); err != nil {
		t.Fatalf("首次 Close 出错: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("重复 Close 应是 no-op 且返回 nil, 实得 %v", err)
	}
}

// --- 写线错误传播 ---

// TestWriteFaithfulRequestPropagatesWriteError 用已关闭的 net.Pipe 触发底层写失败,
// 校验头部值与 body 的写错误都会被向上返回(中途超出 bufio 缓冲触发 flush 到坏连接)。
func TestWriteFaithfulRequestPropagatesWriteError(t *testing.T) {
	t.Run("header value write error", func(t *testing.T) {
		c1, c2 := net.Pipe()
		_ = c2.Close() // 读端关闭 → 写端 flush 必失败
		defer c1.Close()
		pc := &persistConn{conn: c1}
		big := strings.Repeat("v", 1<<16) // 远超 bufio 默认缓冲,迫使写头时即 flush
		req := mkReq(t, "GET", "http://h/", nil, nil)
		if err := writeFaithfulRequest(pc, req, [][2]string{{"X-Big", big}}, nil, false); err == nil {
			t.Fatalf("向已关闭连接写头应返回错误")
		}
	})

	t.Run("body write error", func(t *testing.T) {
		c1, c2 := net.Pipe()
		_ = c2.Close()
		defer c1.Close()
		pc := &persistConn{conn: c1}
		body := []byte(strings.Repeat("b", 1<<16)) // 头很小走缓冲,大 body 写时 flush 失败
		req := mkReq(t, "POST", "http://h/", body, nil)
		if err := writeFaithfulRequest(pc, req, [][2]string{{"Host", "h"}}, body, false); err == nil {
			t.Fatalf("向已关闭连接写 body 应返回错误")
		}
	})
}

// --- 网络:直连/握手错误回退 ---

func TestDirectDialErrorFallsBack(t *testing.T) {
	fb := &recordRT{}
	tr := New(Config{Fallback: fb, DialTimeout: time.Second})
	ordered := [][2]string{{"Host", "127.0.0.1:1"}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://127.0.0.1:1/", nil, ordered) // 1 端口通常无监听
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("建连失败应回退而非报错: %v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("直连失败应回退, 实得调用 %d 次", fb.called)
	}
}

// TestRoundTripWriteErrorRetriesNewConn 覆盖 RoundTrip 的写失败重试分支:从空闲池取到的
// 连接底层已死,首次写线即失败;因来自空闲池,应换一条新连接重试并最终成功。
func TestRoundTripWriteErrorRetriesNewConn(t *testing.T) {
	echo := newRawEcho(t)
	tr := New(Config{Fallback: &errRT{}})
	key := connKey(mustURL(t, "http://"+echo.addr()+"/"), nil)

	// 注入一条底层已关闭的“空闲”连接:bufio 写在 Flush 时打到坏连接 → 写失败。
	c1, c2 := net.Pipe()
	_ = c1.Close()
	_ = c2.Close()
	dead := &persistConn{conn: c1, br: bufio.NewReader(c1), key: key, idleAt: time.Now()}
	tr.mu.Lock()
	tr.idle[key] = []*persistConn{dead}
	tr.mu.Unlock()

	ordered := [][2]string{{"Host", echo.addr()}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://"+echo.addr()+"/", nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("写失败后应换新连接重试成功: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	<-echo.heads
}

// --- 网络:经上游代理 ---

// TestHTTPViaProxyUsesAbsoluteForm 覆盖明文 http 经代理:直接走代理、请求行用绝对形式、
// 无 CONNECT。同时覆盖 connKey 的带代理分支与 dial 的代理-http 分支。
func TestHTTPViaProxyUsesAbsoluteForm(t *testing.T) {
	proxy := newRawEcho(t) // 充当上游代理后端
	proxyURL := mustURL(t, "http://"+proxy.addr())
	tr := New(Config{
		Fallback: &errRT{},
		Proxy:    func(*http.Request) (*url.URL, error) { return proxyURL, nil },
	})
	ordered := [][2]string{{"Host", "origin.example"}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://origin.example/path?q=1", nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("经代理 http RoundTrip: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	got := <-proxy.heads
	if !strings.HasPrefix(got, "GET http://origin.example/path?q=1 HTTP/1.1\r\n") {
		t.Fatalf("代理应收到绝对形式请求行, 实得首行: %q", strings.SplitN(got, "\r\n", 2)[0])
	}
}

func TestProxyDialErrorFallsBack(t *testing.T) {
	fb := &recordRT{}
	proxyURL := mustURL(t, "http://127.0.0.1:1") // 无监听 → 连代理即失败
	tr := New(Config{
		Fallback:    fb,
		Proxy:       func(*http.Request) (*url.URL, error) { return proxyURL, nil },
		DialTimeout: time.Second,
	})
	ordered := [][2]string{{"Host", "origin.example"}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://origin.example/", nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("连代理失败应回退: %v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("连代理失败应回退, 实得调用 %d 次", fb.called)
	}
}

// prefixConn 让 TLS server 先消费 bufio 已缓冲的字节,再读底层连接,写入仍走底层连接。
type prefixConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *prefixConn) Read(b []byte) (int, error) { return p.r.Read(b) }

// connectProxy 是支持 CONNECT 隧道的最小代理:校验 Proxy-Authorization、回 200,
// 随后在隧道上以给定证书充当 TLS 源站,回显隧道内请求头并回 200。
type connectProxy struct {
	ln       net.Listener
	cert     tls.Certificate
	heads    chan string
	authSeen chan string
}

func newConnectProxy(t *testing.T, cert tls.Certificate) *connectProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cp := &connectProxy{ln: ln, cert: cert, heads: make(chan string, 4), authSeen: make(chan string, 4)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go cp.handle(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return cp
}

func (cp *connectProxy) addr() string { return cp.ln.Addr().String() }

func (cp *connectProxy) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)

	reqLine, auth := "", ""
	for i := 0; ; i++ {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if i == 0 {
			reqLine = strings.TrimSpace(line)
		}
		if strings.HasPrefix(strings.ToLower(line), "proxy-authorization:") {
			auth = strings.TrimSpace(line[len("proxy-authorization:"):])
		}
		if line == "\r\n" {
			break
		}
	}
	cp.authSeen <- auth
	if !strings.HasPrefix(reqLine, "CONNECT ") {
		_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}
	if _, err := c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}

	tc := tls.Server(&prefixConn{Conn: c, r: br}, &tls.Config{
		Certificates: []tls.Certificate{cp.cert},
		NextProtos:   []string{"http/1.1"},
	})
	if err := tc.Handshake(); err != nil {
		return
	}
	tbr := bufio.NewReader(tc)
	var head strings.Builder
	for {
		line, err := tbr.ReadString('\n')
		if err != nil {
			return
		}
		head.WriteString(line)
		if line == "\r\n" {
			break
		}
	}
	cp.heads <- head.String()
	_, _ = tc.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
}

// TestHTTPSViaProxyConnect 覆盖 https 经上游代理的完整路径:CONNECT 隧道(含 Basic
// 代理鉴权)、隧道内 TLS 握手与保真写线。同时覆盖 proxyConnect、basicAuth 与 canonicalAddr
// 的默认 443 端口分支。
func TestHTTPSViaProxyConnect(t *testing.T) {
	cert := selfSignedCert(t)
	proxy := newConnectProxy(t, cert)
	proxyURL := mustURL(t, "http://user:pass@"+proxy.addr())
	tr := New(Config{
		Fallback:        &errRT{},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Proxy:           func(*http.Request) (*url.URL, error) { return proxyURL, nil },
		DialTimeout:     2 * time.Second,
		TLSTimeout:      2 * time.Second,
	})
	ordered := [][2]string{{"Host", "orig.example"}, {"User-Agent", "UA"}}
	req := mkReq(t, "GET", "https://orig.example/secure", nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("https 经代理 RoundTrip: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if auth := <-proxy.authSeen; auth != wantAuth {
		t.Fatalf("代理鉴权头不符: got %q, want %q", auth, wantAuth)
	}
	got := <-proxy.heads
	if !strings.HasPrefix(got, "GET /secure HTTP/1.1\r\nHost: orig.example\r\nUser-Agent: UA\r\n") {
		t.Fatalf("隧道内请求头序列/大小写不符: %q", got)
	}
}

// TestHTTPSViaProxyConnectRejected 覆盖 proxyConnect 中 CONNECT 非 200 的错误分支:
// 代理拒绝建隧道 → dial 报错 → 回退。
func TestHTTPSViaProxyConnectRejected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" {
						break
					}
				}
				_, _ = c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
			}(c)
		}
	}()

	fb := &recordRT{}
	proxyURL := mustURL(t, "http://"+ln.Addr().String())
	tr := New(Config{
		Fallback:        fb,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Proxy:           func(*http.Request) (*url.URL, error) { return proxyURL, nil },
		DialTimeout:     2 * time.Second,
		TLSTimeout:      2 * time.Second,
	})
	ordered := [][2]string{{"Host", "orig.example"}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "https://orig.example/", nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("CONNECT 被拒应回退: %v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("CONNECT 失败应回退, 实得调用 %d 次", fb.called)
	}
}

// TestResponseCaptureFilled 覆盖 readResponse 回填 flow.ResponseCapture 的分支:
// ctx 装入收集器后,上游响应的原始状态行与头序列应被写入。
func TestResponseCaptureFilled(t *testing.T) {
	echo := newRawEcho(t)
	tr := New(Config{Fallback: &errRT{}})
	rc := &flow.ResponseCapture{}
	ordered := [][2]string{{"Host", echo.addr()}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://"+echo.addr()+"/", nil, ordered)
	req = req.WithContext(flow.WithResponseCapture(req.Context(), rc))

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	<-echo.heads

	if rc.StatusLine != "HTTP/1.1 200 OK" {
		t.Fatalf("状态行未回填: %q", rc.StatusLine)
	}
	found := false
	for _, kv := range rc.Headers {
		if strings.EqualFold(kv[0], "Content-Length") {
			found = true
		}
	}
	if !found {
		t.Fatalf("响应头序列未回填: %v", rc.Headers)
	}
}
