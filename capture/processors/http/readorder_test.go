// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// pipeReader 把一段字节通过真实 TCP 连接喂给 bufio.Reader(模拟客户端到代理的连接),
// 以便复现「小请求无 body 时 Peek 不应死等」的真实阻塞行为。
func pipeReader(t *testing.T, data string, keepOpen bool) *bufio.Reader {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		_, _ = io.WriteString(c, data)
		if !keepOpen {
			_ = c.Close()
		} else {
			// 保持连接打开:模拟客户端发完头后等待响应(无 body 的请求)。
			time.AfterFunc(2*time.Second, func() { _ = c.Close() })
		}
	}()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return bufio.NewReaderSize(conn, 64*1024)
}

func TestPeekParsePreservesOrderAndCasing(t *testing.T) {
	raw := "GET /p?z=1&a=2 HTTP/1.1\r\n" +
		"Host: h.example\r\n" +
		"User-Agent: App/1\r\n" +
		"x-custom-token: ABC\r\n" +
		"X-Request-ID: 42\r\n" +
		"accept-encoding: gzip, br\r\n" +
		"\r\n"
	br := bufio.NewReaderSize(strings.NewReader(raw), 64*1024)
	got := peekRequestHeaderOrder(br)
	want := [][2]string{
		{"Host", "h.example"},
		{"User-Agent", "App/1"},
		{"x-custom-token", "ABC"},
		{"X-Request-ID", "42"},
		{"accept-encoding", "gzip, br"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("解析头序列不符:\n got=%v\nwant=%v", got, want)
	}
	// Peek 不应消费:ReadRequest 仍能正常解析同一 reader。
	req, err := readRequestPreservingOrder(br)
	if err != nil {
		t.Fatalf("ReadRequest after peek: %v", err)
	}
	if req.URL.RawQuery != "z=1&a=2" {
		t.Fatalf("RawQuery 应逐字保留, 实得 %q", req.URL.RawQuery)
	}
	rawHeaders, ok := flow.RawHeadersFrom(req.Context())
	if !ok || !reflect.DeepEqual(rawHeaders, want) {
		t.Fatalf("ctx 中的 RawHeaders 不符: %v (ok=%v)", rawHeaders, ok)
	}
}

// TestPeekSmallRequestNoBodyNoDeadlock 复现关键风险:无 body 的小请求,Peek 不能死等更多字节。
func TestPeekSmallRequestNoBodyNoDeadlock(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: h\r\nAccept: */*\r\n\r\n"
	br := pipeReader(t, raw, true /*keepOpen: 客户端发完头后不关、等响应*/)

	done := make(chan [][2]string, 1)
	go func() { done <- peekRequestHeaderOrder(br) }()
	select {
	case got := <-done:
		want := [][2]string{{"Host", "h"}, {"Accept", "*/*"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("小请求解析不符: %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("peekRequestHeaderOrder 在无 body 小请求上死等(死锁)")
	}
}

// TestPeekObsFold 容忍 obs-fold 续行(并入上一个值)。
func TestPeekObsFold(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: h\r\nX-Long: part1\r\n  part2\r\n\r\n"
	br := bufio.NewReaderSize(strings.NewReader(raw), 64*1024)
	got := peekRequestHeaderOrder(br)
	want := [][2]string{{"Host", "h"}, {"X-Long", "part1 part2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("obs-fold 解析不符: %v", got)
	}
}

// TestPeekOversizedHeaderGivesUp 头块超过可 Peek 上限时返回 nil(放弃保真,回退标准转发)。
func TestPeekOversizedHeaderGivesUp(t *testing.T) {
	var b strings.Builder
	b.WriteString("GET / HTTP/1.1\r\nHost: h\r\n")
	big := strings.Repeat("x", 200*1024) // 远超 64KB 缓冲
	b.WriteString("X-Big: " + big + "\r\n\r\n")
	// reader 缓冲设为 64KB(与生产一致),头块超过它 → 放弃。
	br := bufio.NewReaderSize(strings.NewReader(b.String()), 64*1024)
	if got := peekRequestHeaderOrder(br); got != nil {
		t.Fatalf("超大头块应放弃保真(返回 nil), 实得 %d 项", len(got))
	}
}

// TestPeekDuplicateHeaders 保留重复头的各次出现与顺序。
func TestPeekDuplicateHeaders(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: h\r\nCookie: a=1\r\nCookie: b=2\r\n\r\n"
	br := bufio.NewReaderSize(strings.NewReader(raw), 64*1024)
	got := peekRequestHeaderOrder(br)
	want := [][2]string{{"Host", "h"}, {"Cookie", "a=1"}, {"Cookie", "b=2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("重复头不符: %v", got)
	}
}
