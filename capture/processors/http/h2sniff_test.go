// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// h2ClientBytes 生成一个 h2 客户端开头发出的字节：连接前言、SETTINGS、WINDOW_UPDATE，
// 然后是各个请求的 HEADERS 帧。
func h2ClientBytes(t testing.TB, padded bool, requests ...[]hpack.HeaderField) []byte {
	t.Helper()
	var wire bytes.Buffer
	wire.WriteString(http2.ClientPreface)
	framer := http2.NewFramer(&wire, nil)
	framer.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: 1 << 20})
	framer.WriteWindowUpdate(0, 1<<20)

	var block bytes.Buffer
	encoder := hpack.NewEncoder(&block)
	for i, fields := range requests {
		block.Reset()
		for _, field := range fields {
			encoder.WriteField(field)
		}
		params := http2.HeadersFrameParam{
			StreamID:      uint32(2*i + 1),
			BlockFragment: block.Bytes(),
			EndStream:     true,
			EndHeaders:    true,
		}
		if padded {
			params.PadLength = 7
			params.Priority = http2.PriorityParam{StreamDep: 0, Weight: 15}
		}
		if err := framer.WriteHeaders(params); err != nil {
			t.Fatal(err)
		}
	}
	return wire.Bytes()
}

func h2Fields(method, scheme, authority string) []hpack.HeaderField {
	fields := []hpack.HeaderField{{Name: ":method", Value: method}}
	if scheme != "" {
		fields = append(fields, hpack.HeaderField{Name: ":scheme", Value: scheme}, hpack.HeaderField{Name: ":path", Value: "/"})
	}
	return append(fields,
		hpack.HeaderField{Name: ":authority", Value: authority},
		hpack.HeaderField{Name: "user-agent", Value: "test"},
		hpack.HeaderField{Name: "cookie", Value: strings.Repeat("c", 300)},
	)
}

func TestH2FirstRequest(t *testing.T) {
	cases := []struct {
		name   string
		padded bool
		fields []hpack.HeaderField
		want   string
	}{
		{"https 请求", false, h2Fields("GET", "https", "mitm.test"), "https"},
		{"http 请求", false, h2Fields("GET", "http", "target.test"), "http"},
		{"CONNECT", false, h2Fields("CONNECT", "", "target.test:443"), ""},
		{"带填充与优先级的 HEADERS 帧", true, h2Fields("POST", "http", "target.test"), "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 后面跟着的第二个请求不影响结果。
			wire := h2ClientBytes(t, tc.padded, tc.fields, h2Fields("GET", "https", "other.test"))

			// 不论字节怎样分批到达，结果都一样。
			for _, chunk := range []int{1, 7, 64, len(wire)} {
				var first h2FirstRequest
				done := false
				for rest := wire; len(rest) > 0 && !done; {
					n := min(chunk, len(rest))
					if _, ok := first.scheme(); ok {
						t.Fatalf("每批 %d 字节：解析结束前不应有结果", chunk)
					}
					done = first.feed(rest[:n])
					rest = rest[n:]
				}
				if !done {
					t.Fatalf("每批 %d 字节：读完首个请求后应结束解析", chunk)
				}
				if got, ok := first.scheme(); !ok || got != tc.want {
					t.Fatalf("每批 %d 字节：首个请求的 :scheme = %q（%v），期望 %q", chunk, got, ok, tc.want)
				}
			}
		})
	}
}

// 头块超过最大帧长时客户端把它拆进 CONTINUATION 帧，:path 排在前面的客户端（curl、Go）
// 遇到长 URL 时，:scheme 与 :authority 会落在后续帧里，拆分点还可能在字段中间。
func TestH2FirstRequestContinuation(t *testing.T) {
	var block bytes.Buffer
	encoder := hpack.NewEncoder(&block)
	for _, field := range []hpack.HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":path", Value: "/?q=" + strings.Repeat("x", 300)},
		{Name: ":scheme", Value: "https"},
		{Name: ":authority", Value: "mitm.test"},
		{Name: "user-agent", Value: "test"},
	} {
		encoder.WriteField(field)
	}
	fragment := block.Bytes()

	for _, split := range []int{1, len(fragment) / 2, len(fragment) - 3} {
		var wire bytes.Buffer
		wire.WriteString(http2.ClientPreface)
		framer := http2.NewFramer(&wire, nil)
		framer.WriteSettings()
		framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: fragment[:split], EndStream: true})
		framer.WriteContinuation(1, false, fragment[split:split+1])
		framer.WriteContinuation(1, true, fragment[split+1:])

		for _, chunk := range []int{1, 64, wire.Len()} {
			var first h2FirstRequest
			done := false
			for rest := wire.Bytes(); len(rest) > 0 && !done; {
				n := min(chunk, len(rest))
				done = first.feed(rest[:n])
				rest = rest[n:]
			}
			if got, ok := first.scheme(); !done || !ok || got != "https" {
				t.Fatalf("在第 %d 字节拆分、每批 %d 字节：首个请求的 :scheme = %q（结束 %v），期望 https", split, chunk, got, done)
			}
		}
	}
}

func TestH2FirstRequestGivesUp(t *testing.T) {
	t.Run("不是 HTTP/2", func(t *testing.T) {
		var first h2FirstRequest
		if !first.feed([]byte("GET / HTTP/1.1\r\nHost: mitm.test\r\n\r\n")) {
			t.Fatal("前言不符时应立即结束解析")
		}
		if _, ok := first.scheme(); ok {
			t.Fatal("前言不符时不应有结果")
		}
	})
	t.Run("迟迟没有 HEADERS 帧", func(t *testing.T) {
		var wire bytes.Buffer
		wire.WriteString(http2.ClientPreface)
		framer := http2.NewFramer(&wire, nil)
		for wire.Len() < h2SniffLimit {
			framer.WritePing(false, [8]byte{})
		}
		var first h2FirstRequest
		if !first.feed(wire.Bytes()) {
			t.Fatal("超过暂存上限后应结束解析")
		}
		if _, ok := first.scheme(); ok || first.buf != nil {
			t.Fatal("放弃解析后不应有结果，也不应留着暂存的字节")
		}
	})
	t.Run("帧长超过上限", func(t *testing.T) {
		var first h2FirstRequest
		wire := append([]byte(http2.ClientPreface), 0xff, 0xff, 0xff, byte(http2.FrameSettings), 0, 0, 0, 0, 0)
		if !first.feed(wire) {
			t.Fatal("帧头声明的长度超过上限时应立即结束解析")
		}
		if _, ok := first.scheme(); ok {
			t.Fatal("放弃解析后不应有结果")
		}
	})
	t.Run("未启用", func(t *testing.T) {
		var first *h2FirstRequest
		if _, ok := first.scheme(); ok {
			t.Fatal("nil 上不应有结果")
		}
	})
}

// 逐字节到达是解析开销的最坏情况：每次送入都只多一个字节。
func BenchmarkH2FirstRequestByteByByte(b *testing.B) {
	request := h2ClientBytes(b, false, h2Fields("GET", "https", "mitm.test"))
	// 声明长度在 http2 服务端的帧长上限内、负载迟迟不到齐的 SETTINGS 帧。
	stalled := append([]byte(http2.ClientPreface), 0, 0x80, 0, byte(http2.FrameSettings), 0, 0, 0, 0, 0)
	zero := []byte{0}
	cases := []struct {
		name string
		next func(i int) []byte
	}{
		{"首个请求", func(i int) []byte { return request[i : i+1] }},
		{"不完整的大帧", func(i int) []byte {
			if i < len(stalled) {
				return stalled[i : i+1]
			}
			return zero
		}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var first h2FirstRequest
				for i := 0; !first.feed(tc.next(i)); i++ {
				}
			}
		})
	}
}
