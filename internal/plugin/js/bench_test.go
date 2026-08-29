// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package js

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// 测量一次插件调用的完整通道开销，包括序列化、邮箱往返、VM JSON 往返及回写。
// 基准插件不包含脚本业务逻辑，超时设置为 30s 以覆盖大载荷。

// benchPlugin 建一个钩子体为空的插件,用于度量通道固定开销。
func benchPlugin(b *testing.B, src string) *Plugin {
	b.Helper()
	p, err := NewPlugin(Config{ID: "bench", Source: src, Enabled: true, Timeout: 30 * time.Second}, nil)
	if err != nil {
		b.Fatalf("NewPlugin: %v", err)
	}
	b.Cleanup(p.Close)
	return p
}

// benchText 生成 n 字节 ASCII 载荷。
func benchText(n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('a' + i%26)
	}
	return buf
}

// benchCJK 生成以三字节汉字为主的合法 UTF-8 载荷。
func benchCJK(n int) []byte {
	buf := []byte(strings.Repeat("中文载荷abc", n/8+1))
	return buf[:n/3*3]
}

// benchBinary 生成由孤立续字节组成的非法 UTF-8 载荷，代表 b64 通道输入。
func benchBinary(n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(0x80 + i%64)
	}
	return buf
}

var benchSizes = []struct {
	name string
	size int
}{
	{"0B", 0},
	{"1KB", 1 << 10},
	{"64KB", 64 << 10},
	{"1MB", 1 << 20},
}

func benchReqFlow(body []byte) *flow.Flow {
	return &flow.Flow{
		ID: "bench-flow",
		Request: &flow.Request{
			Method: "POST",
			URL:    "http://example.com/api/bench",
			Host:   "example.com",
			Path:   "/api/bench",
			Header: map[string][]string{
				"Content-Type": {"application/octet-stream"},
				"Accept":       {"*/*"},
				"User-Agent":   {"sniffy-bench"},
			},
			Body: body,
		},
	}
}

// runOnRequest 测量 OnRequest 热路径，并在每轮恢复相同的 body 与 Modified 状态。
func runOnRequest(b *testing.B, gen func(int) []byte) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			p := benchPlugin(b, "function onRequest(f){}")
			payload := gen(sz.size)
			f := benchReqFlow(payload)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f.Request.Body = payload
				f.Modified = false
				p.OnRequest(ctx, f)
			}
		})
	}
}

func BenchmarkOnRequestText(b *testing.B)   { runOnRequest(b, benchText) }
func BenchmarkOnRequestCJK(b *testing.B)    { runOnRequest(b, benchCJK) }
func BenchmarkOnRequestBinary(b *testing.B) { runOnRequest(b, benchBinary) }

// runWebSocket 测量 WebSocket 帧热路径；通道选择由载荷 UTF-8 合法性决定。
func runWebSocket(b *testing.B, msgType string, gen func(int) []byte) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			p := benchPlugin(b, "function onWebSocketMessage(f){}")
			payload := gen(sz.size)
			m := &flow.WSMessage{
				ID:        "bench-msg",
				FlowID:    "bench-flow",
				URL:       "ws://example.com/socket",
				Direction: "client->server",
				Type:      msgType,
				Data:      payload,
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Data = payload
				p.OnWebSocketMessage(ctx, m)
			}
		})
	}
}

func BenchmarkOnWebSocketMessageText(b *testing.B) {
	runWebSocket(b, "text", benchText)
}

func BenchmarkOnWebSocketMessageBinary(b *testing.B) {
	runWebSocket(b, "binary", benchBinary)
}

// 测量头部视图进出 VM 的固定开销，覆盖典型和重型请求头规模。

// benchHeaderNames 使用浏览器请求和代理注入头的常见名称。
var benchHeaderNames = []string{
	"Host", "User-Agent", "Accept", "Accept-Encoding", "Accept-Language",
	"Connection", "Cache-Control", "Cookie", "Referer", "Content-Type",
	"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User",
	"Sec-Ch-Ua", "Sec-Ch-Ua-Mobile", "Sec-Ch-Ua-Platform", "Upgrade-Insecure-Requests",
	"Origin", "Pragma", "If-None-Match", "If-Modified-Since", "Authorization",
	"X-Requested-With", "X-Forwarded-For", "X-Forwarded-Proto", "X-Real-Ip",
	"X-Request-Id", "X-Trace-Id", "X-Correlation-Id",
}

// benchHeaders 按名称生成单值头；invalid 为真时加入 Latin-1 文件名值。
func benchHeaders(n int, invalid bool) map[string][]string {
	h := make(map[string][]string, n+1)
	for i := 0; i < n && i < len(benchHeaderNames); i++ {
		h[benchHeaderNames[i]] = []string{"bench-value-" + benchHeaderNames[i] + "; q=0.9"}
	}
	if invalid {
		h["Content-Disposition"] = []string{"attachment; filename=\"\xe4\xf6\xfc\xff.txt\""}
	}
	return h
}

var benchHeaderCases = []struct {
	name    string
	n       int
	invalid bool
}{
	{"10Valid", 10, false},
	{"30Valid", 30, false},
	{"10With1Invalid", 10, true},
	{"30With1Invalid", 30, true},
}

// runOnRequestHeaders 在每轮恢复同一份原始头 map，基准只统计插件处理开销。
func runOnRequestHeaders(b *testing.B, src string) {
	for _, c := range benchHeaderCases {
		b.Run(c.name, func(b *testing.B) {
			p := benchPlugin(b, src)
			hdr := benchHeaders(c.n, c.invalid)
			f := benchReqFlow(nil)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f.Request.Header = hdr
				f.Modified = false
				p.OnRequest(ctx, f)
			}
		})
	}
}

// BenchmarkOnRequestHeaders 测量不修改头部的插件开销。
func BenchmarkOnRequestHeaders(b *testing.B) {
	runOnRequestHeaders(b, "function onRequest(f){}")
}

// BenchmarkOnRequestHeadersEdit 测量新增一个头时的合并开销。
func BenchmarkOnRequestHeadersEdit(b *testing.B) {
	runOnRequestHeaders(b, "function onRequest(f){f.headers['X-Injected']='1';}")
}

// BenchmarkFlattenHeaders 与 BenchmarkMergeHeaders 单独测量头视图转换与合并。
func BenchmarkFlattenHeaders(b *testing.B) {
	for _, c := range benchHeaderCases {
		b.Run(c.name, func(b *testing.B) {
			hdr := benchHeaders(c.n, c.invalid)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkHeaders = flatten(hdr)
			}
		})
	}
}

var sinkHeaders map[string]string

var sinkMerged map[string][]string

// BenchmarkMergeHeaders 测量脚本原样回传时的合并路径。
func BenchmarkMergeHeaders(b *testing.B) {
	for _, c := range benchHeaderCases {
		b.Run(c.name, func(b *testing.B) {
			hdr := benchHeaders(c.n, c.invalid)
			sent := flatten(hdr)
			edited := flatten(hdr)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkMerged, _ = mergeHeaders(hdr, sent, edited)
			}
		})
	}
}

// BenchmarkMergeHeadersEdited 测量修改一个头时的重建路径。
func BenchmarkMergeHeadersEdited(b *testing.B) {
	for _, c := range benchHeaderCases {
		b.Run(c.name, func(b *testing.B) {
			hdr := benchHeaders(c.n, c.invalid)
			sent := flatten(hdr)
			edited := flatten(hdr)
			edited["X-Injected"] = "1"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkMerged, _ = mergeHeaders(hdr, sent, edited)
			}
		})
	}
}
