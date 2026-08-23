// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
)

// withRawHeaders 挂上读取侧抓到的线缆头序列(顺序 + 原始大小写 + 重复项)。
func withRawHeaders(pairs ...[2]string) flowOpt {
	return func(f *flow.Flow) { f.Request.RawHeaders = pairs }
}

func TestComposeSeedPrefersWireHeaders(t *testing.T) {
	svc := newTestService(t)
	f := newFlow("seed-1",
		withRequest(http.MethodPost, "https://api.example.com/v1/charges?limit=20"),
		withRequestHeader("Accept", "application/json", "text/plain"),
		withRequestHeader("Content-Type", "application/json"),
		withRequestBody([]byte(`{"amount":2000}`)),
		withRawHeaders(
			[2]string{"Host", "api.example.com"},
			[2]string{"content-type", "application/json"},
			[2]string{"Accept", "application/json"},
			[2]string{"Accept", "text/plain"},
		),
	)
	svc.ImportFlowStarted(f)

	seed, ok := svc.ComposeSeed("seed-1")
	if !ok {
		t.Fatal("ComposeSeed 未找到会话")
	}
	want := [][2]string{
		{"Host", "api.example.com"},
		{"content-type", "application/json"},
		{"Accept", "application/json"},
		{"Accept", "text/plain"},
	}
	if !reflect.DeepEqual(seed.Headers, want) {
		t.Fatalf("头部 = %v,期望 %v", seed.Headers, want)
	}
	if seed.Method != http.MethodPost || seed.URL != "https://api.example.com/v1/charges?limit=20" {
		t.Fatalf("请求行 = %s %s", seed.Method, seed.URL)
	}
	if seed.Body != `{"amount":2000}` || seed.BodySize != 15 || seed.BodyBinary {
		t.Fatalf("体 = %q size=%d binary=%v", seed.Body, seed.BodySize, seed.BodyBinary)
	}
}

// RawHeaders 是进管道之前的原始序列,规则 / 插件改的是 Header 与 Host。预填必须交出
// 「当前值 + 原始顺序」:直接回放 RawHeaders 会让草稿混着改写后的 URL/Body 与改写前的头,
// 编辑重发时把旧凭据、旧 Host 又发一遍。
func TestComposeSeedReflectsPipelineRewrites(t *testing.T) {
	svc := newTestService(t)
	f := newFlow("seed-rewritten",
		withRequest(http.MethodGet, "https://api.example.com/v1/ping"),
		withRawHeaders(
			[2]string{"Host", "old.example.com"},
			[2]string{"authorization", "Bearer old-token"},
			[2]string{"X-Drop-Me", "1"},
			[2]string{"Accept", "*/*"},
		),
	)
	// 规则改写后的状态:换掉 Authorization、删掉一个头、加一个原始序列里没有的头。
	f.Request.Host = "api.example.com"
	f.Request.Header = map[string][]string{
		"Authorization": {"Bearer new-token"},
		"Accept":        {"*/*"},
		"X-Added":       {"by-rule"},
	}
	svc.ImportFlowStarted(f)

	seed, ok := svc.ComposeSeed("seed-rewritten")
	if !ok {
		t.Fatal("ComposeSeed 未找到会话")
	}
	want := [][2]string{
		{"Host", "api.example.com"},
		{"authorization", "Bearer new-token"},
		{"Accept", "*/*"},
		{"X-Added", "by-rule"},
	}
	if !reflect.DeepEqual(seed.Headers, want) {
		t.Fatalf("头部 = %v,期望 %v", seed.Headers, want)
	}
}

// h2 入站或头部过大时抓不到线缆序列,只能退回规范化 map;此时按名字排序,
// 保证同一条 flow 每次预填结果一致(map 遍历顺序随机)。
func TestComposeSeedFallsBackToSortedMap(t *testing.T) {
	svc := newTestService(t)
	f := newFlow("seed-2",
		withRequestHeader("X-Trace-Id", "abc"),
		withRequestHeader("Accept", "a", "b"),
		withRequestHeader("Content-Type", "application/json"),
	)
	svc.ImportFlowStarted(f)

	for i := 0; i < 8; i++ {
		seed, ok := svc.ComposeSeed("seed-2")
		if !ok {
			t.Fatal("ComposeSeed 未找到会话")
		}
		want := [][2]string{
			{"Accept", "a"},
			{"Accept", "b"},
			{"Content-Type", "application/json"},
			{"X-Trace-Id", "abc"},
		}
		if !reflect.DeepEqual(seed.Headers, want) {
			t.Fatalf("第 %d 次:头部 = %v,期望 %v", i, seed.Headers, want)
		}
	}
}

// 构造器是文本编辑器,二进制体无法往返;必须如实标注而不是悄悄给个空体。
func TestComposeSeedFlagsBinaryBody(t *testing.T) {
	svc := newTestService(t)
	svc.ImportFlowStarted(newFlow("seed-3", withRequestBody([]byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01})))

	seed, ok := svc.ComposeSeed("seed-3")
	if !ok {
		t.Fatal("ComposeSeed 未找到会话")
	}
	if !seed.BodyBinary {
		t.Fatal("二进制体未被标记")
	}
	if seed.Body != "" {
		t.Fatalf("二进制体不该有文本形式,却得到 %q", seed.Body)
	}
	if seed.BodySize != 6 {
		t.Fatalf("BodySize = %d,期望 6", seed.BodySize)
	}
}

// 超大文本体不能整份塞进编辑器:那是同一份数据在 Go / JSON / Bridge / WebView 里的
// 第四五份副本。按 BodyBinary 的老办法处理——只报大小,明说载不进去。
func TestComposeSeedFlagsOversizedBody(t *testing.T) {
	svc := newTestService(t)
	// 纯文本:IsBinary 通得过,真正拖垮进程的是整体长度而不是内容形态。
	body := bytes.Repeat([]byte("a"), MaxComposeSeedBytes+1)
	svc.ImportFlowStarted(newFlow("seed-big", withRequestBody(body)))

	seed, ok := svc.ComposeSeed("seed-big")
	if !ok {
		t.Fatal("ComposeSeed 未找到会话")
	}
	if !seed.BodyTooLarge {
		t.Fatal("超限的体未被标记 BodyTooLarge")
	}
	if seed.Body != "" {
		t.Fatalf("超限的体不该被带回,却得到 %d 字节", len(seed.Body))
	}
	if seed.BodyBinary {
		t.Fatal("文本体不该同时被标记为二进制")
	}
	if seed.BodySize != len(body) {
		t.Fatalf("BodySize = %d,期望 %d", seed.BodySize, len(body))
	}
}

// 恰好等于上限要能载入,否则「载得进来就发得出去」这条约定在边界上不成立。
func TestComposeSeedAllowsBodyAtLimit(t *testing.T) {
	svc := newTestService(t)
	body := bytes.Repeat([]byte("a"), MaxComposeSeedBytes)
	svc.ImportFlowStarted(newFlow("seed-edge", withRequestBody(body)))

	seed, _ := svc.ComposeSeed("seed-edge")
	if seed.BodyTooLarge || len(seed.Body) != len(body) {
		t.Fatalf("等于上限的体应完整载入: tooLarge=%v len=%d", seed.BodyTooLarge, len(seed.Body))
	}
}

func TestComposeSeedMissingSession(t *testing.T) {
	svc := newTestService(t)
	if seed, ok := svc.ComposeSeed("nope"); ok || seed != nil {
		t.Fatalf("不存在的会话应返回 (nil, false),得到 (%v, %v)", seed, ok)
	}
}

// 构造器发起的 SSE / WebSocket 走 Import* 入口,不受录制开关约束:用户亲手点的请求若
// 因为抓包正暂停而只留下一条没有消息的空壳会话,构造器就等于失灵。
func TestImportSessionsBypassRecording(t *testing.T) {
	svc := newTestService(t)
	rec := newEventRecorder(t, svc.Bus())
	svc.StopRecording()

	svc.RecordStreamSession(&flow.StreamSession{ID: "rec-sse", Kind: flow.StreamSSE, Status: "open"}, nil)
	svc.RecordWSSession(&flow.WSSession{ID: "rec-ws", URL: "wss://x/ws", Status: "open"}, nil)
	if types := rec.types(); len(types) != 0 {
		t.Fatalf("暂停录制时 Record* 不该广播,却得到 %v", types)
	}
	if _, ok := svc.StreamSession("rec-sse"); ok {
		t.Fatal("暂停录制时 RecordStreamSession 不该入库")
	}
	if _, ok := svc.WSSession("rec-ws"); ok {
		t.Fatal("暂停录制时 RecordWSSession 不该入库")
	}

	svc.ImportStreamSession(&flow.StreamSession{
		ID:           "compose-sse",
		URL:          "https://api.example.com/events",
		Kind:         flow.StreamSSE,
		Status:       "open",
		StartTime:    time.Now(),
		MessageCount: 1,
		Messages: []flow.StreamMessage{{
			ID: "m1", FlowID: "compose-sse", Direction: flow.WSServerToClient,
			Kind: flow.StreamSSE, Data: []byte("hello"),
		}},
	}, nil)
	svc.ImportWSSession(&flow.WSSession{
		ID:           "compose-ws",
		URL:          "wss://api.example.com/ws",
		Status:       "open",
		StartTime:    time.Now(),
		MessageCount: 1,
		Messages: []flow.WSMessage{{
			ID: "m1", FlowID: "compose-ws", Direction: flow.WSClientToServer,
			Type: flow.WSText, Data: []byte("ping"),
		}},
	}, nil)

	want := []core.EventType{core.EventStreamMessage, core.EventWSMessage}
	if types := rec.types(); !reflect.DeepEqual(types, want) {
		t.Fatalf("广播序列 = %v,期望 %v", types, want)
	}
	ss, ok := svc.StreamSession("compose-sse")
	if !ok {
		t.Fatal("ImportStreamSession 未入库")
	}
	if len(ss.Messages) != 1 {
		t.Fatalf("流会话消息数 = %d,期望 1", len(ss.Messages))
	}
	ws, ok := svc.WSSession("compose-ws")
	if !ok {
		t.Fatal("ImportWSSession 未入库")
	}
	if len(ws.Messages) != 1 {
		t.Fatalf("WS 会话消息数 = %d,期望 1", len(ws.Messages))
	}
}

// 一条 SSE 流在收到响应头、每批消息到达时都要刷新会话,只有收尾那次能计入统计;
// 若 ImportFlowUpdated 也累加,一次请求会被算成很多次。
func TestImportFlowUpdatedDoesNotDoubleCountStats(t *testing.T) {
	svc := newTestService(t)
	f := newFlow("compose-flow",
		withRequest(http.MethodGet, "https://api.example.com/events"),
		withResponse(http.StatusOK, "text/event-stream", nil),
		withState(flow.StatePending),
	)

	svc.ImportFlowStarted(f)
	svc.ImportFlowUpdated(f)
	svc.ImportFlowUpdated(f)
	if got := svc.Statistics().TotalRequests; got != 0 {
		t.Fatalf("收尾前统计 = %d,期望 0", got)
	}

	f.State = flow.StateCompleted
	svc.ImportFlowCompleted(f)

	stats := svc.Statistics()
	if stats.TotalRequests != 1 {
		t.Fatalf("TotalRequests = %d,期望 1", stats.TotalRequests)
	}
	if got := stats.MethodDistribution[http.MethodGet]; got != 1 {
		t.Fatalf("GET 计数 = %d,期望 1", got)
	}
	if got := stats.StatusCodeDistribution[http.StatusOK]; got != 1 {
		t.Fatalf("200 计数 = %d,期望 1", got)
	}
	if _, n := svc.Sessions(1, 10); n != 1 {
		t.Fatalf("会话数 = %d,期望 1(多次 Import 应更新同一条)", n)
	}
}
