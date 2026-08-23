// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

func TestSSEScannerSplitsAcrossChunks(t *testing.T) {
	s := &flow.SSEScanner{}
	// 分片喂入,跨片的事件应在边界齐全后才产出。
	var events []flow.SSEEvent
	events = append(events, s.Push([]byte("event: greet\nda"))...)
	if len(events) != 0 {
		t.Fatalf("不应在未见空行前产出事件,得 %d", len(events))
	}
	events = append(events, s.Push([]byte("ta: hello\n\n: keep-alive\n\ndata: a\ndata: b\n\n"))...)
	if len(events) != 3 {
		t.Fatalf("期望 3 个事件块(greet / 注释 / 多行 data),得 %d", len(events))
	}
	if events[0].Event != "greet" || string(events[0].Data) != "hello" {
		t.Fatalf("事件0解析错误: event=%q data=%q", events[0].Event, events[0].Data)
	}
	if string(events[1].Data) != "" {
		t.Fatalf("注释块不应有 data,得 %q", events[1].Data)
	}
	if string(events[2].Data) != "a\nb" {
		t.Fatalf("多行 data 应拼接为 a\\nb,得 %q", events[2].Data)
	}
	// Raw 应可保真回放(拼起来等于原始输入)。
	if got := string(events[0].Raw) + string(events[1].Raw) + string(events[2].Raw); got != "event: greet\ndata: hello\n\n: keep-alive\n\ndata: a\ndata: b\n\n" {
		t.Fatalf("Raw 回放不保真: %q", got)
	}
}

func TestSSEScannerCRLF(t *testing.T) {
	s := &flow.SSEScanner{}
	ev := s.Push([]byte("data: x\r\n\r\n"))
	if len(ev) != 1 || string(ev[0].Data) != "x" {
		t.Fatalf("CRLF 边界解析失败: %+v", ev)
	}
}

func TestSSEScannerFlushReturnsLeftover(t *testing.T) {
	s := &flow.SSEScanner{}
	if ev := s.Push([]byte("data: done\n\ndata: half")); len(ev) != 1 {
		t.Fatalf("期望 1 个完整事件,得 %d", len(ev))
	}
	// 残留必须原样回吐:上游 EOF 时客户端还要靠它拿到最后半截字节。
	if got := string(s.Flush()); got != "data: half" {
		t.Fatalf("残留字节不保真: %q", got)
	}
	if got := s.Flush(); len(got) != 0 {
		t.Fatalf("Flush 后缓冲应清空,得 %q", got)
	}
}

// 上游一直不发空行时,缓冲的大小由对端说了算。超过上限即转入 overflow:
// 停止解析,把字节交回调用方处置(抓包侧原样中继,构造器侧中止)。
func TestSSEScannerOverflowStopsParsing(t *testing.T) {
	s := &flow.SSEScanner{}
	chunk := []byte("data: " + strings.Repeat("x", 1<<20))
	for total := 0; total <= flow.MaxSSEEventBytes; total += len(chunk) {
		s.Push(chunk)
	}
	if !s.Overflowed() {
		t.Fatal("单事件超过上限后应转入 overflow")
	}
	// 转入后不再返回事件,但字节一个不少地留在缓冲里等调用方取走。
	if ev := s.Push([]byte("data: more\n\n")); len(ev) != 0 {
		t.Fatalf("overflow 后不该再切出事件,得 %d 个", len(ev))
	}
	if n := len(s.Flush()); n <= flow.MaxSSEEventBytes {
		t.Fatalf("Flush 应交回全部积压字节,得 %d", n)
	}
	// 调用方取走后缓冲清空 —— 每读一块就取一次,内存才封得住。
	if n := len(s.Flush()); n != 0 {
		t.Fatalf("Flush 后缓冲应清空,得 %d", n)
	}
}

// 一次 Push 里塞进多个完整事件时,超限判定只看切剩的尾巴,不能把它们误判成一个超大事件。
func TestSSEScannerOverflowIgnoresCompletedEvents(t *testing.T) {
	s := &flow.SSEScanner{}
	one := "data: " + strings.Repeat("y", 1<<20) + "\n\n"
	var blob strings.Builder
	for blob.Len() <= 2*flow.MaxSSEEventBytes {
		blob.WriteString(one)
	}
	ev := s.Push([]byte(blob.String()))
	if s.Overflowed() {
		t.Fatal("成块的事件已被切走,不该判为 overflow")
	}
	if len(ev) < 2 {
		t.Fatalf("期望切出多个事件,得 %d", len(ev))
	}
}

func TestSSEScannerZeroValueUsable(t *testing.T) {
	// &flow.SSEScanner{} 的调用点(中继循环、测试)都不经构造函数,零值必须直接能用。
	var s flow.SSEScanner
	ev := s.Push([]byte("data: zero\n\n"))
	if len(ev) != 1 || string(ev[0].Data) != "zero" {
		t.Fatalf("零值扫描器解析失败: %+v", ev)
	}
}

func TestIsEventStreamAcceptsParameters(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"TEXT/EVENT-STREAM", true},
		{" text/event-stream ", true},
		{"application/json", false},
		{"", false},
	}
	for _, c := range cases {
		if got := flow.IsEventStream(c.ct); got != c.want {
			t.Fatalf("IsEventStream(%q): got=%v want=%v", c.ct, got, c.want)
		}
	}
}

func TestReserializeSSEOmitsEmptyData(t *testing.T) {
	// 空载荷若被重建成 "data: " 行,等于凭空给客户端塞了一条空消息。
	if got := string(flow.ReserializeSSE("ping", nil)); got != "event: ping\n\n" {
		t.Fatalf("空载荷重建错误: %q", got)
	}
	if got := string(flow.ReserializeSSE("", nil)); got != "\n" {
		t.Fatalf("无 event 名的空载荷重建错误: %q", got)
	}
	if got := string(flow.ReserializeSSE("", []byte("a\nb"))); got != "data: a\ndata: b\n\n" {
		t.Fatalf("多行载荷重建错误: %q", got)
	}
}

func TestReserializeSSERoundTrips(t *testing.T) {
	// 重建出的块必须还能被扫描器切回同一条事件,否则改写过的流会在下游错位。
	raw := flow.ReserializeSSE("greet", []byte("a\nb"))
	ev := (&flow.SSEScanner{}).Push(raw)
	if len(ev) != 1 || ev[0].Event != "greet" || string(ev[0].Data) != "a\nb" {
		t.Fatalf("重建后回扫不一致: %+v", ev)
	}
}

func TestContentTypeBaseStripsParameters(t *testing.T) {
	if got := flow.ContentTypeBase("Application/JSON; charset=UTF-8"); got != "application/json" {
		t.Fatalf("ContentTypeBase: got=%q want=%q", got, "application/json")
	}
	if got := flow.ContentTypeBase(""); got != "" {
		t.Fatalf("空 Content-Type 应得空串,得 %q", got)
	}
}

func BenchmarkSSEScannerPush(b *testing.B) {
	chunk := bytes.Repeat([]byte("event: message\ndata: {\"delta\":\"hello world\"}\n\n"), 8)
	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	for b.Loop() {
		s := &flow.SSEScanner{}
		if len(s.Push(chunk)) != 8 {
			b.Fatal("事件数不符")
		}
		s.Flush()
	}
}
