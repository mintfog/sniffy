// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/pipeline"
)

// captureUpstreamRequest 起一个原始 TCP 上游,读完一个请求的头块原样返回,
// 用于断言 sniffy 实际发往上游的字节(转发忠实度)。
func captureUpstreamRequest(t *testing.T, clientRaw string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		br := bufio.NewReader(c)
		var sb strings.Builder
		for {
			line, err := br.ReadString('\n')
			sb.WriteString(line)
			if line == "\r\n" || err != nil {
				break
			}
		}
		got <- sb.String()
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
	}()

	raw := strings.ReplaceAll(clientRaw, "{HOST}", ln.Addr().String())

	prev := activePipeline
	activePipeline = pipeline.New(nil, nil)
	defer func() { activePipeline = prev }()

	mc := newMockConn(raw)
	srv := newMockServer()
	conn := newMockConnection(mc, srv)
	p := New(conn).(*Processor)
	_ = p.handleHttpProtocol(srv, conn.GetReader(), conn.GetWriter())

	select {
	case s := <-got:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("上游未收到请求")
		return ""
	}
}

// TestForwardPreservesQuery 确认带 query 的 POST 转发到上游时请求行完整保留 query。
func TestForwardPreservesQuery(t *testing.T) {
	raw := strings.Join([]string{
		"POST /api/pay?sign=abc123&ts=1700000000&uid=42 HTTP/1.1",
		"Host: {HOST}",
		"Content-Type: application/json",
		"Content-Length: 2",
		"Connection: close",
		"", "{}",
	}, "\r\n")

	reqLine := strings.SplitN(captureUpstreamRequest(t, raw), "\r\n", 2)[0]
	if !strings.Contains(reqLine, "?sign=abc123&ts=1700000000&uid=42") {
		t.Fatalf("query 未完整透传, 上游请求行: %q", reqLine)
	}
}

// TestForwardNoInjectedAcceptEncoding 确认客户端未发 Accept-Encoding 时,
// sniffy 不会向上游注入(此前 Go Transport 默认注入 gzip,破坏 App 签名校验)。
func TestForwardNoInjectedAcceptEncoding(t *testing.T) {
	raw := strings.Join([]string{
		"POST /api/pay?uid=42 HTTP/1.1",
		"Host: {HOST}",
		"Content-Type: application/json",
		"Content-Length: 2",
		"Connection: close",
		"", "{}",
	}, "\r\n")

	got := captureUpstreamRequest(t, raw)
	if strings.Contains(strings.ToLower(got), "accept-encoding") {
		t.Fatalf("不应向上游注入 Accept-Encoding, 但实际发送了:\n%s", got)
	}
}

// headerNamesWithPrefix 按出现顺序返回头块中名字带 prefix 的头名(大小写不敏感)。
func headerNamesWithPrefix(headerBlock, prefix string) []string {
	var out []string
	for _, line := range strings.Split(headerBlock, "\r\n") {
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			out = append(out, name)
		}
	}
	return out
}

// TestForwardHeaderOrder 固定当前“请求头被重排”的行为——这是一处已知的转发保真度缺口。
//
// 现状:sniffy 经 Go net/http(http.Client.Do)转发,Go 写出请求时会把自定义头按字母序排列,
// 客户端发送的原始顺序丢失(http.ReadRequest 读进 map 时其实已丢序)。而 mitmproxy / Burp 等
// 专业抓包工具刻意保留原序与原始大小写(头顺序是指纹/签名向量),这正是“换个抓包软件 App 就
// 正常”的原因之一。
//
// 本测试断言“被重排成字母序”,以把现状钉住、让回归可见。若将来改为保留原序
//（需绕开 http.Client、按原序裸写请求),把断言翻转为 got == clientOrder 即可。
func TestForwardHeaderOrder(t *testing.T) {
	// 客户端按非字母序发送三个自定义头。
	raw := strings.Join([]string{
		"POST /x HTTP/1.1",
		"Host: {HOST}",
		"X-Zulu: 1",
		"X-Mike: 2",
		"X-Alpha: 3",
		"Content-Length: 0",
		"Connection: close",
		"", "",
	}, "\r\n")

	got := headerNamesWithPrefix(captureUpstreamRequest(t, raw), "X-")

	clientOrder := []string{"X-Zulu", "X-Mike", "X-Alpha"}
	alphabetical := []string{"X-Alpha", "X-Mike", "X-Zulu"}

	if reflect.DeepEqual(got, clientOrder) {
		t.Fatalf("请求头原序已被保留——若已实现保真转发,请把本测试断言改为期望保留原序。got=%v", got)
	}
	if !reflect.DeepEqual(got, alphabetical) {
		t.Fatalf("预期当前实现把自定义头重排为字母序,实际 got=%v", got)
	}
	t.Logf("客户端原序 %v → 上游收到 %v（Go net/http 重排为字母序,原序丢失;mitmproxy/Burp 会保留原序）",
		clientOrder, got)
}
