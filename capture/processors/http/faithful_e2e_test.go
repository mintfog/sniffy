// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/forward"
	"github.com/mintfog/sniffy/internal/pipeline"
)

// TestFaithfulForwardEndToEnd 端到端锁定「无侵入转发」:
// 客户端请求经真实处理器(readRequestPreservingOrder → 管道 → ApplyRequestToHTTP →
// 保真转发器)送到上游后,线上字节的请求头顺序与大小写、参数顺序与客户端原样一致,
// 不排序、不规范化、不注入 User-Agent。
func TestFaithfulForwardEndToEnd(t *testing.T) {
	// 上游:原始 TCP 服务端,记录收到的请求头块原文。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
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
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	}()
	addr := ln.Addr().String()

	// 装配:保真上游客户端 + 无操作管道 + 收集型 sink。保存/恢复包级状态。
	defer func(c *http.Client, p *pipeline.Pipeline, s FlowSink) {
		sharedHttpClient, activePipeline, flowSink = c, p, s
	}(sharedHttpClient, activePipeline, flowSink)
	sharedHttpClient = &http.Client{
		Transport: forward.New(forward.Config{Fallback: &http.Transport{}}),
		Timeout:   5 * time.Second,
	}
	activePipeline = pipeline.New(nil, nil) // 无钩子:一律 Continue,但仍走 ApplyRequestToHTTP
	flowSink = &collectingSink{}

	// 客户端以代理常见的绝对形式发出,头顺序/大小写刻意非常规。
	clientReq := "GET http://" + addr + "/p?z=1&a=2&m=3 HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"User-Agent: MyApp/1.0\r\n" +
		"Accept: */*\r\n" +
		"x-custom-token: ABC\r\n" +
		"X-Request-ID: 42\r\n" +
		"accept-encoding: gzip, br\r\n" +
		"Cookie: sid=xyz\r\n" +
		"\r\n"

	mc := newMockConn(clientReq)
	srv := newMockServer()
	conn := newMockConnection(mc, srv)
	p := New(conn).(*Processor)

	if err := p.handleHttpProtocol(srv, conn.GetReader(), conn.GetWriter()); err != nil {
		t.Fatalf("handleHttpProtocol: %v", err)
	}

	select {
	case got := <-gotHead:
		want := "GET /p?z=1&a=2&m=3 HTTP/1.1\r\n" +
			"Host: " + addr + "\r\n" +
			"User-Agent: MyApp/1.0\r\n" +
			"Accept: */*\r\n" +
			"x-custom-token: ABC\r\n" +
			"X-Request-ID: 42\r\n" +
			"accept-encoding: gzip, br\r\n" +
			"Cookie: sid=xyz\r\n" +
			"\r\n"
		if got != want {
			t.Fatalf("上游收到的线上字节有侵入:\n--- got ---\n%q\n--- want ---\n%q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("上游未在超时内收到请求")
	}

	// 也验证响应已写回客户端。
	if !strings.Contains(mc.WrittenData(), "200 OK") {
		t.Fatalf("应把上游 200 响应写回客户端, 实得: %q", mc.WrittenData())
	}
}

// TestFaithfulResponseWriteBackEndToEnd 端到端锁定「响应写回保真」:上游响应的状态行、
// 响应头顺序/大小写、Content-Encoding 与压缩体,经处理器原样写回客户端,不排序/不重编码。
func TestFaithfulResponseWriteBackEndToEnd(t *testing.T) {
	// gzip 压缩的响应体。
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"ok":true,"msg":"faithful response"}`))
	_ = zw.Close()
	gzBytes := gz.Bytes()

	// 上游:原始 TCP,读完请求头后返回头顺序/大小写刻意非常规 + gzip 体的响应。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	respHead := "HTTP/1.1 200 OK\r\n" +
		"X-Custom-Resp: V1\r\n" +
		"content-type: application/json\r\n" +
		"X-AbC: yes\r\n" +
		"Content-Encoding: gzip\r\n" +
		"Content-Length: " + strconv.Itoa(len(gzBytes)) + "\r\n" +
		"\r\n"
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
		_, _ = c.Write([]byte(respHead))
		_, _ = c.Write(gzBytes)
	}()
	addr := ln.Addr().String()

	defer func(c *http.Client, p *pipeline.Pipeline, s FlowSink) {
		sharedHttpClient, activePipeline, flowSink = c, p, s
	}(sharedHttpClient, activePipeline, flowSink)
	sharedHttpClient = &http.Client{
		Transport: forward.New(forward.Config{Fallback: &http.Transport{}}),
		Timeout:   5 * time.Second,
	}
	activePipeline = pipeline.New(nil, nil) // 无钩子:响应不被改动 → 应原样回放
	flowSink = &collectingSink{}

	clientReq := "GET http://" + addr + "/data HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"User-Agent: MyApp/1.0\r\n" +
		"Accept-Encoding: gzip\r\n" +
		"\r\n"
	mc := newMockConn(clientReq)
	srv := newMockServer()
	conn := newMockConnection(mc, srv)
	p := New(conn).(*Processor)

	if err := p.handleHttpProtocol(srv, conn.GetReader(), conn.GetWriter()); err != nil {
		t.Fatalf("handleHttpProtocol: %v", err)
	}

	// 客户端收到的线上字节应与上游响应逐字一致(头序列+大小写+状态行+gzip 体)。
	want := respHead + string(gzBytes)
	if got := mc.WrittenData(); got != want {
		t.Fatalf("响应写回有侵入:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}
