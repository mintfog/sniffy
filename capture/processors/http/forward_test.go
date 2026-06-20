// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/forward"
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

	// 装配生产同款的保真上游客户端,使断言反映真实转发行为(引擎在生产中即如此注入)。
	prevPipe, prevClient := activePipeline, sharedHttpClient
	activePipeline = pipeline.New(nil, nil)
	sharedHttpClient = &http.Client{
		Transport: forward.New(forward.Config{Fallback: &http.Transport{DisableCompression: true}}),
		Timeout:   5 * time.Second,
	}
	defer func() { activePipeline, sharedHttpClient = prevPipe, prevClient }()

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

// TestForwardHeaderOrder 锁定「无侵入转发」:请求头按客户端原始顺序透传到上游,不被重排。
//
// 历史缺口:sniffy 曾经 Go net/http(http.Client.Do)转发,Go 写出请求时把自定义头按字母序
// 排列,客户端原始顺序丢失(http.ReadRequest 读进 map 时即丢序)。而 mitmproxy / Burp 等专业
// 抓包工具刻意保留原序与原始大小写(头顺序是指纹/签名向量),这正是「换个抓包软件 App 就正常」
// 的原因之一。现已通过 internal/forward 的保真转发器(读取侧抓原序、出站侧按原序裸写)修复。
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
	if !reflect.DeepEqual(got, clientOrder) {
		t.Fatalf("保真转发应保留客户端原始头顺序 %v, 实得 %v", clientOrder, got)
	}
}
