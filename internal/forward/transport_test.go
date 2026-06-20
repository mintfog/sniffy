// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package forward

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// errRT 是一个永远报错的回退 RoundTripper:用于断言「本应走保真路径」时没有误走回退。
type errRT struct{ called int32 }

func (e *errRT) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("fallback should not be used")
}

// recordRT 记录回退是否被调用,并代答一个 200。
type recordRT struct {
	mu     sync.Mutex
	called int
}

func (r *recordRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.called++
	r.mu.Unlock()
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{
		StatusCode: 200, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req,
	}, nil
}

// rawEcho 是一个原始 TCP 回显服务端:逐个请求读出「请求行+头块」原样推回 channel,
// 读取并丢弃 body(按 Content-Length),回 200,并在同一连接上循环(支持 keep-alive)。
type rawEcho struct {
	ln    net.Listener
	heads chan string
}

func newRawEcho(t *testing.T) *rawEcho {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	e := &rawEcho{ln: ln, heads: make(chan string, 16)}
	go e.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return e
}

func (e *rawEcho) serve() {
	for {
		c, err := e.ln.Accept()
		if err != nil {
			return
		}
		go e.handle(c)
	}
}

func (e *rawEcho) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	for {
		var head bytes.Buffer
		cl := 0
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			head.WriteString(line)
			if t := strings.TrimSpace(line); strings.HasPrefix(strings.ToLower(t), "content-length:") {
				cl = atoiSafe(strings.TrimSpace(t[len("content-length:"):]))
			}
			if line == "\r\n" {
				break
			}
		}
		if cl > 0 {
			if _, err := io.CopyN(io.Discard, br, int64(cl)); err != nil {
				return
			}
		}
		e.heads <- head.String()
		if _, err := c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")); err != nil {
			return
		}
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func (e *rawEcho) addr() string { return e.ln.Addr().String() }

func mkReq(t *testing.T, method, url string, body []byte, ordered [][2]string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.ContentLength = int64(len(body))
	return req.WithContext(flow.WithOrderedHeaders(req.Context(), ordered))
}

// TestFaithfulPreservesOrderAndCasing 是核心断言:线上字节的头顺序与大小写与客户端原样一致,
// 不排序、不规范化、不注入 UA、参数顺序逐字保留。
func TestFaithfulPreservesOrderAndCasing(t *testing.T) {
	echo := newRawEcho(t)
	fb := &errRT{}
	tr := New(Config{Fallback: fb})

	ordered := [][2]string{
		{"Host", echo.addr()},
		{"User-Agent", "MyApp/1.0"},
		{"Accept", "*/*"},
		{"x-custom-token", "ABC"},
		{"X-Request-ID", "42"},
		{"accept-encoding", "gzip, br"},
		{"Cookie", "sid=xyz"},
	}
	req := mkReq(t, "GET", "http://"+echo.addr()+"/p?z=1&a=2&m=3", nil, ordered)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	want := "GET /p?z=1&a=2&m=3 HTTP/1.1\r\n" +
		"Host: " + echo.addr() + "\r\n" +
		"User-Agent: MyApp/1.0\r\n" +
		"Accept: */*\r\n" +
		"x-custom-token: ABC\r\n" +
		"X-Request-ID: 42\r\n" +
		"accept-encoding: gzip, br\r\n" +
		"Cookie: sid=xyz\r\n" +
		"\r\n"
	got := <-echo.heads
	if got != want {
		t.Fatalf("线上字节不一致:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestFaithfulPostBodyFraming 校验带 body 的 POST:Content-Length 正确、body 完整、保序。
func TestFaithfulPostBodyFraming(t *testing.T) {
	echo := newRawEcho(t)
	tr := New(Config{Fallback: &errRT{}})
	body := []byte("k1=v1&k2=v2")
	ordered := [][2]string{
		{"Host", echo.addr()},
		{"content-type", "application/x-www-form-urlencoded"},
		{"Content-Length", "11"},
	}
	req := mkReq(t, "POST", "http://"+echo.addr()+"/submit", body, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	got := <-echo.heads
	want := "POST /submit HTTP/1.1\r\n" +
		"Host: " + echo.addr() + "\r\n" +
		"content-type: application/x-www-form-urlencoded\r\n" +
		"Content-Length: 11\r\n" +
		"\r\n"
	if got != want {
		t.Fatalf("POST 头不一致:\n got=%q\nwant=%q", got, want)
	}
}

// TestKeepAliveReuse 校验同一目标连续两次请求复用了同一条连接(连接池工作)。
func TestKeepAliveReuse(t *testing.T) {
	echo := newRawEcho(t)
	tr := New(Config{Fallback: &errRT{}})

	doOne := func() {
		ordered := [][2]string{{"Host", echo.addr()}, {"User-Agent", "x"}}
		req := mkReq(t, "GET", "http://"+echo.addr()+"/", nil, ordered)
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		<-echo.heads
	}
	doOne()
	// 第一次结束后,连接应已回到空闲池。
	key := connKey(mustURL(t, "http://"+echo.addr()+"/"), nil)
	tr.mu.Lock()
	idleN := len(tr.idle[key])
	tr.mu.Unlock()
	if idleN != 1 {
		t.Fatalf("期望 1 条空闲连接被复用, 实得 %d", idleN)
	}
	doOne()
}

// TestFallbackWhenNoOrderedHeaders 校验缺少保真头序列时回退到标准 RoundTripper。
func TestFallbackWhenNoOrderedHeaders(t *testing.T) {
	fb := &recordRT{}
	tr := New(Config{Fallback: fb})
	req, _ := http.NewRequest("GET", "http://example.invalid/", nil) // 无 ordered ctx
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("期望回退被调用 1 次, 实得 %d", fb.called)
	}
}

// TestDisabledForcesFallback 校验 Disabled 开关下一律走回退。
func TestDisabledForcesFallback(t *testing.T) {
	fb := &recordRT{}
	tr := New(Config{Fallback: fb, Disabled: true})
	ordered := [][2]string{{"Host", "example.invalid"}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://example.invalid/", nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("Disabled 应走回退, 实得调用 %d 次", fb.called)
	}
}

// TestUpgradeForcesFallback 校验 WebSocket 升级请求不走保真转发。
func TestUpgradeForcesFallback(t *testing.T) {
	fb := &recordRT{}
	tr := New(Config{Fallback: fb})
	ordered := [][2]string{{"Host", "x"}, {"Upgrade", "websocket"}, {"Connection", "Upgrade"}}
	req := mkReq(t, "GET", "http://x/", nil, ordered)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("Upgrade 应走回退, 实得 %d", fb.called)
	}
}

// TestLargeBodyFallsBack 校验超过 MaxFaithfulBody 的请求体回退到标准 Transport(防流控死锁)。
func TestLargeBodyFallsBack(t *testing.T) {
	fb := &recordRT{}
	tr := New(Config{Fallback: fb, MaxFaithfulBody: 1024})
	body := bytes.Repeat([]byte("a"), 2048) // 超阈值
	ordered := [][2]string{{"Host", "h"}, {"Content-Length", "2048"}}
	req := mkReq(t, "POST", "http://h/upload", body, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("超大 body 应回退, 实得调用 %d 次", fb.called)
	}
}

// TestHTTPSFaithfulRoundTrip 校验直连 https(本地自签 + ALPN http/1.1)下保真转发可用。
func TestHTTPSFaithfulRoundTrip(t *testing.T) {
	cert := selfSignedCert(t)
	gotHead := make(chan string, 1)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	})
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		var head bytes.Buffer
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
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	}()

	tr := New(Config{
		Fallback:        &errRT{},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	})
	ordered := [][2]string{{"Host", ln.Addr().String()}, {"sec-ch-ua", "\"X\""}, {"User-Agent", "UA"}}
	req := mkReq(t, "GET", "https://"+ln.Addr().String()+"/secure", nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("https RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	got := <-gotHead
	if !strings.HasPrefix(got, "GET /secure HTTP/1.1\r\nHost: "+ln.Addr().String()+"\r\nsec-ch-ua: \"X\"\r\nUser-Agent: UA\r\n") {
		t.Fatalf("https 头序列/大小写不符: %q", got)
	}
}

// TestFallbackOnTLSHandshakeFailure 校验对「只说 http/1.1 会握手失败」的目标(模拟 h2-only)
// 会回退到标准 RoundTripper,而非把错误返回客户端。
func TestFallbackOnTLSHandshakeFailure(t *testing.T) {
	// 一个普通 TCP 服务端,收到 TLS ClientHello 后直接关闭 → 握手失败。
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	fb := &recordRT{}
	tr := New(Config{Fallback: fb, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialTimeout: 2 * time.Second, TLSTimeout: 2 * time.Second})
	ordered := [][2]string{{"Host", ln.Addr().String()}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "https://"+ln.Addr().String()+"/", nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("应回退而非报错, 实得 err=%v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("TLS 握手失败应回退, 实得调用 %d 次", fb.called)
	}
}

// dropSecondServer 对每条连接:第 1 个请求正常回 200(使连接进入空闲池),
// 第 2 个请求读完后直接关闭、不响应(模拟空闲 keep-alive 连接被服务端回收的竞态)。
// 它记录每个被完整读到的请求方法,供断言「是否发生重发」。
type dropSecondServer struct {
	ln  net.Listener
	mu  sync.Mutex
	log []string
}

func newDropSecondServer(t *testing.T) *dropSecondServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &dropSecondServer{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *dropSecondServer) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	for n := 1; ; n++ {
		method := ""
		cl := 0
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if method == "" {
				method = strings.Fields(line)[0]
			}
			if t := strings.TrimSpace(line); strings.HasPrefix(strings.ToLower(t), "content-length:") {
				cl = atoiSafe(strings.TrimSpace(t[len("content-length:"):]))
			}
			if line == "\r\n" {
				break
			}
		}
		if cl > 0 {
			if _, err := io.CopyN(io.Discard, br, int64(cl)); err != nil {
				return
			}
		}
		s.mu.Lock()
		s.log = append(s.log, method)
		s.mu.Unlock()
		if n >= 2 {
			return // 第 2 个请求:读完即关,不响应
		}
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	}
}

func (s *dropSecondServer) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.log {
		if m == method {
			n++
		}
	}
	return n
}

func (s *dropSecondServer) addr() string { return s.ln.Addr().String() }

// TestReplaySafety 校验空闲连接竞态下的重放策略:GET(幂等)可换连接重试;
// POST(非幂等)整请求已发出却未得响应时不得重发,返回错误。
func TestReplaySafety(t *testing.T) {
	warmup := func(tr *Transport, s *dropSecondServer) {
		ordered := [][2]string{{"Host", s.addr()}, {"User-Agent", "x"}}
		req := mkReq(t, "GET", "http://"+s.addr()+"/", nil, ordered)
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatalf("warmup: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	t.Run("GET retries", func(t *testing.T) {
		s := newDropSecondServer(t)
		tr := New(Config{Fallback: &errRT{}})
		warmup(tr, s) // 连接进入空闲池
		ordered := [][2]string{{"Host", s.addr()}, {"User-Agent", "x"}}
		req := mkReq(t, "GET", "http://"+s.addr()+"/probe", nil, ordered)
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatalf("幂等 GET 应重试成功, 实得 err=%v", err)
		}
		_ = resp.Body.Close()
		if got := s.count("GET"); got < 3 { // warmup + 被丢弃的那次 + 重试成功的那次
			t.Fatalf("GET 应被重发, 服务端共见 GET %d 次(期望>=3)", got)
		}
	})

	t.Run("POST does not replay", func(t *testing.T) {
		s := newDropSecondServer(t)
		tr := New(Config{Fallback: &errRT{}})
		warmup(tr, s) // 连接进入空闲池
		body := []byte("x=1")
		ordered := [][2]string{{"Host", s.addr()}, {"User-Agent", "x"}, {"Content-Length", "3"}}
		req := mkReq(t, "POST", "http://"+s.addr()+"/probe", body, ordered)
		_, err := tr.RoundTrip(req)
		if err == nil {
			t.Fatalf("非幂等 POST 在响应丢失时应返回错误而非重发")
		}
		if got := s.count("POST"); got != 1 {
			t.Fatalf("POST 不得重发, 服务端应只见 1 次, 实得 %d", got)
		}
	})
}

// TestPreservesRequestHTTPVersion 校验请求行的 HTTP 版本按客户端原样透传(1.0 不被升级成 1.1)。
func TestPreservesRequestHTTPVersion(t *testing.T) {
	echo := newRawEcho(t)
	tr := New(Config{Fallback: &errRT{}})
	ordered := [][2]string{{"Host", echo.addr()}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://"+echo.addr()+"/v", nil, ordered)
	req.Proto, req.ProtoMajor, req.ProtoMinor = "HTTP/1.0", 1, 0

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	got := <-echo.heads
	if !strings.HasPrefix(got, "GET /v HTTP/1.0\r\n") {
		t.Fatalf("应保留客户端 HTTP/1.0 版本, 实得请求行: %q", strings.SplitN(got, "\r\n", 2)[0])
	}
}

// newStallBodyServer 返回一个上游:发完响应头与部分 body 后永久挂起(不再发数据、也不关连接),
// 用于验证读体阶段对 ctx 取消的响应——裸 conn.Read 否则会永久阻塞。
func newStallBodyServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
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
				// 声明 100 字节 body,只发 10 字节后挂起到测试结束。
				_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n0123456789"))
				<-done
			}(c)
		}
	}()
	t.Cleanup(func() { close(done); _ = ln.Close() })
	return ln
}

// TestBodyReadHonorsContextCancel 校验:上游发完头后在 body 中途挂起时,ctx 取消能打断
// 阻塞的 body 读取(裸 conn 不感知 ctx,依赖 connGuard 关连接),不致 goroutine / 连接泄漏。
func TestBodyReadHonorsContextCancel(t *testing.T) {
	ln := newStallBodyServer(t)
	tr := New(Config{Fallback: &errRT{}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ordered := [][2]string{{"Host", ln.Addr().String()}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://"+ln.Addr().String()+"/", nil, ordered)
	req = req.WithContext(flow.WithOrderedHeaders(ctx, ordered))

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	readErr := make(chan error, 1)
	go func() {
		_, e := io.ReadAll(resp.Body)
		readErr <- e
	}()

	// body 未发完,读取应阻塞;取消 ctx 前不应返回。
	select {
	case e := <-readErr:
		t.Fatalf("ctx 未取消前 body 读取不应返回, 实得 err=%v", e)
	case <-time.After(150 * time.Millisecond):
	}
	// 取消后应迅速解除阻塞(任意错误均可)。
	cancel()
	select {
	case <-readErr:
	case <-time.After(2 * time.Second):
		t.Fatalf("ctx 取消后 body 读取仍阻塞 —— connGuard 未生效")
	}
	_ = resp.Body.Close()
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return u
}

// selfSignedCert 生成一张供本地 TLS 测试用的自签证书。
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return cert
}
