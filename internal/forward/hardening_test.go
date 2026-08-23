// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package forward

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestRedirectDoesNotReplayOrderedHeaders 锁住:跨主机重定向不得重放上一跳的有序头。
// http.Client 的重定向循环把原 ctx 原样交给合成请求,里面的 ordered 仍是旧主机那一份,
// 逐字重放会把旧 Host 连同 Authorization / Cookie 送给新主机 —— 而这正是 net/http
// 自己跟随时会剥离的东西。这里故意用默认(跟随)策略,断言 forward 自己就扛得住。
func TestRedirectDoesNotReplayOrderedHeaders(t *testing.T) {
	type seen struct{ host, auth, cookie string }
	got := make(chan seen, 4)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{host: r.Host, auth: r.Header.Get("Authorization"), cookie: r.Header.Get("Cookie")}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	// 目标写成 localhost、来源是 127.0.0.1:只有主机名不同才构成「跨站」。
	// net/http 的 shouldCopyHeaderOnRedirect 按 Hostname() 比对,仅端口不同是不剥离的。
	tu, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	targetHost := "localhost:" + tu.Port()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+targetHost+"/next", http.StatusFound)
	}))
	defer origin.Close()
	originHost := strings.TrimPrefix(origin.URL, "http://")

	req := mkReq(t, http.MethodGet, origin.URL+"/start", nil, [][2]string{
		{"Host", originHost},
		{"authorization", "Bearer SECRET"},
		{"Cookie", "session=deadbeef"},
	})
	req.Host = originHost
	req.Header.Set("Authorization", "Bearer SECRET")
	req.Header.Set("Cookie", "session=deadbeef")

	resp, err := (&http.Client{Transport: New(Config{Fallback: http.DefaultTransport})}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	s := <-got
	if s.host != targetHost {
		t.Errorf("重定向目标看到的 Host = %q,期望 %q —— 重放旧 Host 会让 vhost 直接 421", s.host, targetHost)
	}
	if s.auth != "" || s.cookie != "" {
		t.Errorf("跨主机重定向泄漏了凭据: Authorization=%q Cookie=%q", s.auth, s.cookie)
	}
}

// TestRejectsCRLFInOrderedHeaders 锁住:保真写线不再放行含 CR/LF/NUL 的头。
// 绕开 http.Transport 也就绕开了它的头部校验,一个换行足以拼出第二个完整请求(请求拆分),
// 而多出来的那个响应会留在空闲连接里,串到下一条复用它的 flow 上。
func TestRejectsCRLFInOrderedHeaders(t *testing.T) {
	echo := newRawEcho(t)
	tr := New(Config{Fallback: &errRT{}}) // 一旦误走回退,errRT 会让断言看到完全不同的错误

	for _, c := range []struct {
		name    string
		ordered [][2]string
	}{
		{"值里的 CRLF", [][2]string{{"Host", echo.addr()}, {"X-A", "1\r\nContent-Length: 0\r\n\r\nGET /admin HTTP/1.1"}}},
		{"名里的 CRLF", [][2]string{{"Host", echo.addr()}, {"X-A: 1\r\nX-Injected", "2"}}},
		{"值里的 NUL", [][2]string{{"Host", echo.addr()}, {"X-A", "1\x00"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := tr.RoundTrip(mkReq(t, http.MethodGet, "http://"+echo.addr()+"/p", nil, c.ordered))
			if err == nil {
				t.Fatal("含 CR/LF/NUL 的头应被拒绝")
			}
			if !strings.Contains(err.Error(), "CR/LF/NUL") {
				t.Fatalf("错误未说明原因: %v", err)
			}
		})
	}
}

// endlessChunked 是一个永不结束的 chunked 服务端:发完响应头后按 tick 持续吐块,
// 直到被关连接。用来模拟「读取方主动早停,而上游还剩一大截」。
func endlessChunked(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

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
			if strings.TrimRight(line, "\r\n") == "" {
				break
			}
		}
		if _, err := c.Write([]byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n")); err != nil {
			return
		}
		chunk := []byte("400\r\n" + strings.Repeat("x", 1024) + "\r\n")
		for {
			if _, err := c.Write(chunk); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return ln.Addr().String()
}

// TestEarlyCloseDoesNotDrain 锁住:读取方主动早停之后,Close 必须立即返回。
//
// net/http 的响应体 Close 会为复用连接把剩余字节整个抽干,而 pooledBody 此刻已经解除了
// ctx 守护 —— 抽干期间 ctx 取消与 Client.Timeout 都打断不了它。构造器的响应体上限
// (app.cappedBody)与 SSE 的用户停止都是主动早停,上游只要慢一点或者不结束,
// 调用方就会连同这条连接一起被钉死。
func TestEarlyCloseDoesNotDrain(t *testing.T) {
	addr := endlessChunked(t)
	tr := New(Config{Fallback: &errRT{}}) // 误走回退就看不到 pooledBody 的行为了

	resp, err := tr.RoundTrip(mkReq(t, http.MethodGet, "http://"+addr+"/stream", nil, [][2]string{{"Host", addr}}))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	// 只读一点点就收手,模拟触到上限 / 用户点了停止。
	if _, err := io.ReadFull(resp.Body, make([]byte, 512)); err != nil {
		t.Fatalf("读响应体: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = resp.Body.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close 阻塞超过 2s —— 正在抽干上游剩余 body,而此时已无任何超时能打断它")
	}
}
