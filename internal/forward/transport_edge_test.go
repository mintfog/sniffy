// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package forward

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// --- writeFaithfulRequest:各写入点的错误传播 ---

var errFakeWrite = errors.New("fake conn write failure")

// failWriteConn 的写入恒失败,并留存被写入的字节以供断言失败发生在哪个写入点。
// 只实现 writeFaithfulRequest 用到的 Write/Close,其余 net.Conn 方法保持 nil 嵌入 ——
// 一旦被调用会 panic,从而暴露误用。
type failWriteConn struct {
	net.Conn
	written []byte
	writes  int
}

func (c *failWriteConn) Write(p []byte) (int, error) {
	c.writes++
	c.written = append(c.written, p...)
	return 0, errFakeWrite
}

func (c *failWriteConn) Close() error { return nil }

// TestWriteFaithfulRequestErrorAtEachWritePoint 逐一把首次 flush 顶到请求序列化的每个写入点,
// 确认底层写错误被原样上抛 —— 任一处被吞掉,调用方就会在半截请求上读响应。光断言错误不够:
// bufio 的错误是粘滞的,末尾 Flush 会重放它,五个用例返回值完全一样,故同时断言落到连接上的字节。
func TestWriteFaithfulRequestErrorAtEachWritePoint(t *testing.T) {
	bufSize := bufio.NewWriter(io.Discard).Available() // bufio 默认缓冲大小,决定 flush 时机
	const lineOverhead = len("GET  HTTP/1.1\r\n")      // 请求行中除 target 外的固定字节

	cases := []struct {
		name        string
		pathLen     int
		ordered     [][2]string
		lineTooLong bool // 失败点就在请求行(其余用例失败点在头部)
	}{
		{
			name:        "request line exceeds buffer",
			pathLen:     bufSize, // 请求行整体超缓冲 → 直写连接
			ordered:     [][2]string{{"Host", "h"}},
			lineTooLong: true,
		},
		{
			name:    "header name exceeds remaining buffer",
			pathLen: 1,
			ordered: [][2]string{{strings.Repeat("N", bufSize), "v"}},
		},
		{
			name:    "flush lands on colon-space",
			pathLen: 1,
			// 请求行 + 头名恰好填满缓冲 → 写 ": " 时触发 flush
			ordered: [][2]string{{strings.Repeat("N", bufSize-1-lineOverhead), "v"}},
		},
		{
			name:    "flush lands on header CRLF",
			pathLen: 1,
			// 请求行 + 头名 + ": " + 头值恰好填满缓冲 → 写头部结尾 CRLF 时触发 flush
			ordered: [][2]string{{"Name", strings.Repeat("v", bufSize-1-lineOverhead-len("Name")-2)}},
		},
		{
			name:    "flush lands on final blank line",
			pathLen: 1,
			// 整个头块恰好填满缓冲 → 写终止空行时触发 flush
			ordered: [][2]string{{"Name", strings.Repeat("v", bufSize-1-lineOverhead-len("Name")-4)}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fc := &failWriteConn{}
			pc := &persistConn{conn: fc}
			req := mkReq(t, "GET", "http://h.example"+pathOfLen(c.pathLen), nil, nil)

			line := "GET " + req.URL.RequestURI() + " HTTP/1.1\r\n"
			if len(line) != c.pathLen+lineOverhead {
				t.Fatalf("用例前提失效:请求行长度 %d, 期望 %d", len(line), c.pathLen+lineOverhead)
			}
			if tooLong := len(line) > bufSize; tooLong != c.lineTooLong {
				t.Fatalf("用例前提失效:请求行超缓冲=%v, 期望 %v", tooLong, c.lineTooLong)
			}

			err := writeFaithfulRequest(pc, req, c.ordered, nil, false)
			if !errors.Is(err, errFakeWrite) {
				t.Fatalf("底层写错误应原样上抛, 实得 %v", err)
			}

			// 期望的完整线上字节;失败前应恰好落盘 wantN 字节(超缓冲的请求行整行直写,
			// 其余用例都是缓冲刚被填满时触发首次 flush)。
			full := line
			for _, kv := range c.ordered {
				full += kv[0] + ": " + kv[1] + "\r\n"
			}
			full += "\r\n"
			wantN := bufSize
			if c.lineTooLong {
				wantN = len(line)
			}
			if fc.writes != 1 {
				t.Fatalf("首次写失败后 bufio 不应再写连接, 实得 %d 次写入", fc.writes)
			}
			if string(fc.written) != full[:wantN] {
				t.Fatalf("失败点不在预期写入点:落盘 %d 字节(期望 %d);尾部实得 %q, 期望 %q",
					len(fc.written), wantN, tailSnippet(string(fc.written)), tailSnippet(full[:wantN]))
			}
		})
	}
}

// pathOfLen 构造长度恰为 n 的 origin-form 路径。
func pathOfLen(n int) string { return "/" + strings.Repeat("a", n-1) }

// tailSnippet 截出末尾若干字节,用于失败信息(整段前缀有数千字节,直接打印无法阅读)。
func tailSnippet(s string) string {
	const n = 24
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// --- peekResponseHead:头块超缓冲 ---

// TestPeekResponseHeadGivesUpOnOversizedHeadBlock 覆盖 64KB 上限:头块塞满整个读缓冲
// 仍未见终止空行时放弃保真抓取,且必须不推进读指针(否则 http.ReadResponse 会读到半截头)。
func TestPeekResponseHeadGivesUpOnOversizedHeadBlock(t *testing.T) {
	const bufSize = 64 * 1024
	filler := []byte("X-Pad: 0123456789\r\n") // 不含空行,永远凑不出头块结尾
	data := bytes.Repeat(filler, bufSize/len(filler)+2)
	br := bufio.NewReaderSize(bytes.NewReader(data), bufSize)

	status, hdr := peekResponseHead(br)
	if status != "" || hdr != nil {
		t.Fatalf("超缓冲头块应放弃抓取, 实得 status=%q hdr=%v", status, hdr)
	}
	if br.Buffered() != bufSize {
		t.Fatalf("Peek 不应消费字节, 期望缓冲满 %d, 实得 %d", bufSize, br.Buffered())
	}
	head, err := br.Peek(len(filler))
	if err != nil || !bytes.Equal(head, filler) {
		t.Fatalf("读指针被推进了: head=%q err=%v", head, err)
	}
}

// --- proxyConnect:写失败与响应不可解析 ---

func TestProxyConnectWriteError(t *testing.T) {
	c1, c2 := net.Pipe()
	_ = c2.Close() // 对端关闭 → 写 CONNECT 必失败
	defer c1.Close()

	err := proxyConnect(c1, "orig.example:443", mustURL(t, "http://p.example:3128"), time.Second)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("写 CONNECT 失败应上抛底层错误, 实得 %v", err)
	}
}

// TestProxyConnectUnreadableResponse 覆盖 CONNECT 响应无法解析的两种情形。
// 与「非 200」不同:此时连状态码都拿不到,错误必须来自解析层而非状态判定。
func TestProxyConnectUnreadableResponse(t *testing.T) {
	cases := []struct {
		name  string
		reply string // 空串表示读完请求即关闭
		check func(t *testing.T, err error)
	}{
		{
			name: "closed before reply",
			check: func(t *testing.T, err error) {
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("提前 EOF 应上抛 ErrUnexpectedEOF, 实得 %v", err)
				}
			},
		},
		{
			name:  "garbage status line",
			reply: "NOT-HTTP AT ALL\r\n\r\n",
			check: func(t *testing.T, err error) {
				if err == nil {
					t.Fatalf("畸形状态行应报错")
				}
				if strings.Contains(err.Error(), "CONNECT 失败") {
					t.Fatalf("错误应来自解析层而非状态码判定: %v", err)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c1, c2 := net.Pipe()
			defer c1.Close()
			go func() {
				defer c2.Close()
				br := bufio.NewReader(c2)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" {
						break
					}
				}
				if c.reply != "" {
					_, _ = c2.Write([]byte(c.reply))
				}
			}()

			err := proxyConnect(c1, "orig.example:443", mustURL(t, "http://p.example:3128"), 5*time.Second)
			c.check(t, err)
		})
	}
}

// --- dial:隧道建成后 TLS 握手失败 ---

// TestHTTPSViaProxyTLSFailureFallsBack 覆盖 dial 的「CONNECT 成功但隧道内 TLS 握手失败」
// 分支:代理回 200 后直接断开,握手必败;请求尚未发出,应回退而非报错。
func TestHTTPSViaProxyTLSFailureFallsBack(t *testing.T) {
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
				_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
			}(c)
		}
	}()

	fb := &recordRT{}
	proxyURL := mustURL(t, "http://"+ln.Addr().String())
	tr := New(Config{
		Fallback:        fb,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Proxy:           func(*http.Request) (*url.URL, error) { return proxyURL, nil },
		DialTimeout:     5 * time.Second,
		TLSTimeout:      5 * time.Second,
	})
	ordered := [][2]string{{"Host", "orig.example"}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "https://orig.example/", nil, ordered)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("隧道内握手失败应回退, 实得 err=%v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("握手失败应回退, 实得调用 %d 次", fb.called)
	}
}

// --- RoundTrip:ctx 取消与重试耗尽 ---

// injectDeadIdle 往空闲池塞入 n 条底层已关闭的连接:取用时写线必失败,
// 用于稳定复现「空闲 keep-alive 连接已被对端回收」的竞态。
func injectDeadIdle(t *testing.T, tr *Transport, key string, n int) {
	t.Helper()
	list := make([]*persistConn, 0, n)
	for i := 0; i < n; i++ {
		c1, c2 := net.Pipe()
		_ = c1.Close()
		_ = c2.Close()
		list = append(list, &persistConn{conn: c1, br: bufio.NewReader(c1), key: key, idleAt: time.Now()})
	}
	tr.mu.Lock()
	tr.idle[key] = list
	tr.mu.Unlock()
}

// TestRoundTripWriteErrorOnCanceledContext 覆盖写线失败且 ctx 已取消的分支:
// 此时不得重试、也不得回退(回退会在已死的 ctx 上再发一次),应直接返回 ctx 错误。
func TestRoundTripWriteErrorOnCanceledContext(t *testing.T) {
	tr := New(Config{Fallback: &errRT{}}) // 一旦回退,errRT 会让断言看到别的错误
	target := "http://h.example/p"
	injectDeadIdle(t, tr, connKey(mustURL(t, target), nil), 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ordered := [][2]string{{"Host", "h.example"}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", target, nil, ordered)
	req = req.WithContext(flow.WithOrderedHeaders(ctx, ordered))

	resp, err := tr.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 已取消时应返回 ctx 错误, 实得 %v", err)
	}
}

// TestRoundTripReadErrorOnCanceledContext 覆盖等响应头期间 ctx 被取消的分支:
// 请求已完整发出,连接被 connGuard 打断导致读失败,此时应返回 ctx 错误而非重试/回退。
func TestRoundTripReadErrorOnCanceledContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	gotReq := make(chan struct{})
	done := make(chan struct{})
	defer close(done)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
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
		close(gotReq) // 请求已完整送达 → 写线阶段确已结束,取消只会命中读响应头
		<-done        // 始终不回响应
	}()

	tr := New(Config{Fallback: &errRT{}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ordered := [][2]string{{"Host", ln.Addr().String()}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", "http://"+ln.Addr().String()+"/", nil, ordered)
	req = req.WithContext(flow.WithOrderedHeaders(ctx, ordered))

	errc := make(chan error, 1)
	go func() {
		resp, err := tr.RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		errc <- err
	}()

	<-gotReq
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("读响应头期间 ctx 取消应返回 ctx 错误, 实得 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("ctx 取消后 RoundTrip 仍阻塞 —— connGuard 未覆盖读响应头阶段")
	}
}

// TestRoundTripRetryExhaustionFallsBack 覆盖两次尝试都拿到坏空闲连接的情形:
// 重试次数用尽后应回退到标准 Transport,而不是把错误抛给调用方。
func TestRoundTripRetryExhaustionFallsBack(t *testing.T) {
	fb := &recordRT{}
	tr := New(Config{Fallback: fb})
	target := "http://h.example/p"
	injectDeadIdle(t, tr, connKey(mustURL(t, target), nil), 2)

	ordered := [][2]string{{"Host", "h.example"}, {"User-Agent", "x"}}
	req := mkReq(t, "GET", target, nil, ordered)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("重试耗尽应回退, 实得 err=%v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("重试耗尽应回退 1 次, 实得 %d", fb.called)
	}
	tr.mu.Lock()
	n := len(tr.idle[connKey(mustURL(t, target), nil)])
	tr.mu.Unlock()
	if n != 0 {
		t.Fatalf("坏连接应被逐条取走且不回池, 实得池中仍有 %d 条", n)
	}
}

// TestRoundTripNewConnWriteErrorFallsBack 覆盖「新建连接上写线失败」分支:上游 accept 后
// 立刻 RST,写一个远超内核发送缓冲的请求体必在中途失败;因请求未完整送达,应回退重发。
func TestRoundTripNewConnWriteErrorFallsBack(t *testing.T) {
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
			if tc, ok := c.(*net.TCPConn); ok {
				_ = tc.SetLinger(0) // 关闭时发 RST 而非 FIN,让后续写立即失败
			}
			_ = c.Close()
		}
	}()

	fb := &recordRT{}
	const bodySize = 16 << 20 // 远大于任何合理的 socket 发送缓冲
	tr := New(Config{Fallback: fb, MaxFaithfulBody: bodySize * 2})
	body := bytes.Repeat([]byte("x"), bodySize)
	ordered := [][2]string{{"Host", ln.Addr().String()}, {"Content-Length", "16777216"}}
	req := mkReq(t, "POST", "http://"+ln.Addr().String()+"/upload", body, ordered)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("新连接写失败应回退, 实得 err=%v", err)
	}
	_ = resp.Body.Close()
	if fb.called != 1 {
		t.Fatalf("新连接写失败应回退 1 次, 实得 %d", fb.called)
	}
}
