// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

// sseServer 用 emit 逐块发送并刷新响应，done 结束响应。
// 测试可在响应结束前核对已记录的消息，验证增量读取。
func sseServer(t *testing.T, status int, contentType string) (url string, emit func(string), done func()) {
	t.Helper()
	chunks := make(chan string)
	finished := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		w.(http.Flusher).Flush()
		for {
			select {
			case c := <-chunks:
				_, _ = io.WriteString(w, c)
				w.(http.Flusher).Flush()
			case <-finished:
				return
			case <-r.Context().Done():
				return
			}
		}
	}))

	var once sync.Once
	done = func() { once.Do(func() { close(finished) }) }
	// Cleanup 按注册顺序逆序执行，须先结束响应，srv.Close 才能退出。
	t.Cleanup(srv.Close)
	t.Cleanup(done)

	emit = func(c string) {
		select {
		case chunks <- c:
		case <-time.After(5 * time.Second):
			t.Errorf("向服务端投递数据块超时: %q", c)
		}
	}
	return srv.URL, emit, done
}

// waitStreamSession 收到增量通知后从存储读取完整会话，直到满足 want。
// 须在 SendRequest 前订阅：事件总线不补发历史事件，慢订阅者可能丢消息。
func waitStreamSession(t *testing.T, app *App, ch <-chan core.Event, id string, what string, want func(service.StreamSessionDTOType) bool) service.StreamSessionDTOType {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != core.EventStreamMessage {
				continue
			}
			d, ok := ev.Payload.(service.StreamDeltaDTO)
			if !ok || d.Session.ID != id {
				continue
			}
			if dto, found := app.Service.StreamSession(id); found && want(dto) {
				return dto
			}
		case <-deadline:
			t.Fatalf("等待流会话超时: %s", what)
			return service.StreamSessionDTOType{}
		}
	}
}

// waitFlowSettled 等待请求进入终态；SSE 收到响应头时也会发布更新，须跳过 pending 状态。
func waitFlowSettled(t *testing.T, ch <-chan core.Event, id string) service.HTTPSessionDTO {
	t.Helper()
	// 为 -race 下扫描大体积 SSE 缓冲预留时间。
	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != core.EventFlowUpdated {
				continue
			}
			dto, ok := ev.Payload.(service.HTTPSessionDTO)
			if !ok || dto.ID != id || dto.Status == "pending" {
				continue
			}
			return dto
		case <-deadline:
			t.Fatal("等待 flow 收尾超时")
			return service.HTTPSessionDTO{}
		}
	}
}

func sseSpec(url string, headers [][2]string) flow.RequestSpec {
	return flow.RequestSpec{Kind: flow.SpecKindSSE, Method: "GET", URL: url, Headers: headers}
}

func TestComposeSSERecordsCommentsAndControlBlocks(t *testing.T) {
	app := newComposeApp(t)
	hook := &streamHook{}
	app.Pipeline.RegisterCore(hook)
	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{Method: "POST", URL: "http://example.test/events"}
	rec := newComposeStreamRecorder(app.Service, f, flow.StreamSSE)
	body := io.MultiReader(
		strings.NewReader(": pi"),
		strings.NewReader("ng\n\nretry: 3000\n\nid: 7\n\nevent: ping\n\nevent: empty\ndata:\n\n"),
		strings.NewReader(": ping\r\n\r\nevent: progress\ndata: hello\n\n: ping\n\n"),
	)
	if err := app.pumpComposeSSE(t.Context(), rec, f, body, true); err != nil {
		t.Fatal(err)
	}
	rec.close()
	session, ok := app.Service.StreamSession(f.ID)
	if !ok || session.MessageCount != 8 || len(session.Messages) != 8 {
		t.Fatalf("应记录 8 条 SSE 记录，实际 %+v", session)
	}
	want := []struct{ kind, event, data string }{
		{flow.SSEComment, "", ": ping\n\n"},
		{flow.SSEControl, "", "retry: 3000\n\n"},
		{flow.SSEControl, "", "id: 7\n\n"},
		{flow.SSEControl, "ping", "event: ping\n\n"},
		{"", "empty", ""},
		{flow.SSEComment, "", ": ping\r\n\r\n"},
		{"", "progress", "hello"},
		{flow.SSEComment, "", ": ping\n\n"},
	}
	var total int64
	for i, expected := range want {
		got := session.Messages[i]
		size := int64(len(expected.data))
		total += size
		if got.SSEType != expected.kind || got.EventType != expected.event || got.Data != expected.data || got.Seq != i || got.Size != size || got.Timestamp == "" {
			t.Errorf("记录 %d = %+v，期望 %+v", i, got, expected)
		}
	}
	if session.TotalSize != total || hook.calls.Load() != 2 {
		t.Fatalf("记录字节数=%d，钩子调用=%d，期望 %d 和 2", session.TotalSize, hook.calls.Load(), total)
	}
}

// BenchmarkPumpComposeSSE 使用 nil 记录器，仅度量解析和可选的管道调用。
func BenchmarkPumpComposeSSE(b *testing.B) {
	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{Method: "GET", URL: "http://example.test/events"}
	body := strings.Repeat(": ping\n\nevent: delta\ndata: hello\n\n", 8)
	for _, viaPipeline := range []bool{false, true} {
		name := "直接读取"
		if viaPipeline {
			name = "经过管道"
		}
		b.Run(name, func(b *testing.B) {
			app := &App{Pipeline: pipeline.New(nil, nil)}
			app.Pipeline.RegisterCore(&streamHook{})
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				if err := app.pumpComposeSSE(b.Context(), nil, f, strings.NewReader(body), viaPipeline); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestSendRequestSSERecordsStreamSessionIncrementally(t *testing.T) {
	app := newComposeApp(t)
	url, emit, done := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}

	emit(": ping\n\n")
	first := waitStreamSession(t, app, events, id, "第 1 条消息", func(d service.StreamSessionDTOType) bool {
		return d.MessageCount >= 1
	})
	if first.Status != "open" {
		t.Fatalf("流未结束时状态应为 open,实际 %q", first.Status)
	}
	if first.Kind != flow.StreamSSE {
		t.Fatalf("流类型 = %q,期望 %q", first.Kind, flow.StreamSSE)
	}
	if first.StatusCode != 200 {
		t.Fatalf("状态码 = %d,期望 200", first.StatusCode)
	}
	if first.Method != "GET" || first.URL != url {
		t.Fatalf("方法/URL = %q %q,期望 GET %q", first.Method, first.URL, url)
	}
	if first.Messages[0].Data != ": ping\n\n" || first.Messages[0].SSEType != flow.SSEComment {
		t.Fatalf("首条心跳记录 = %+v", first.Messages[0])
	}

	emit("event: e\ndata: two\n\n")
	second := waitStreamSession(t, app, events, id, "第 2 条消息", func(d service.StreamSessionDTOType) bool {
		return d.MessageCount >= 2
	})
	if second.MessageCount != 2 {
		t.Fatalf("消息数 = %d,期望 2", second.MessageCount)
	}
	if second.Messages[1].EventType != "e" || second.Messages[1].Data != "two" {
		t.Fatalf("第 2 条 = %+v", second.Messages[1])
	}
	if second.Messages[1].Seq != 1 {
		t.Fatalf("第 2 条 seq = %d,期望 1", second.Messages[1].Seq)
	}

	done()
	final := waitStreamSession(t, app, events, id, "会话收尾", func(d service.StreamSessionDTOType) bool {
		return d.Status == "closed"
	})
	if final.EndTime == "" {
		t.Fatal("收尾的会话应带 EndTime")
	}
}

// SSE 消息保存在流会话中，Flow 在持续接收期间保持 awaiting_response。
func TestSendRequestSSEDoesNotBufferBody(t *testing.T) {
	app := newComposeApp(t)
	url, emit, _ := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	emit("data: one\n\n")
	waitStreamSession(t, app, events, id, "第 1 条消息", func(d service.StreamSessionDTOType) bool {
		return d.MessageCount >= 1
	})

	f, ok := app.Service.RawFlow(id)
	if !ok {
		t.Fatal("flow 未入库")
	}
	if f.Response == nil {
		t.Fatal("已收到响应头,Response 不该为空")
	}
	if len(f.Response.Body) != 0 {
		t.Fatalf("流式响应体不该缓冲进 Flow.Body,实际 %d 字节", len(f.Response.Body))
	}
	if f.State != flow.StateAwaitingResponse {
		t.Fatalf("流未结束时状态 = %q,期望 %q", f.State, flow.StateAwaitingResponse)
	}
}

func TestSendRequestSSEFlowLifecycle(t *testing.T) {
	app := newComposeApp(t)
	url, emit, done := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	emit("data: one\n\n")
	done()
	waitFlowSettled(t, events, id)

	f, ok := app.Service.RawFlow(id)
	if !ok {
		t.Fatal("flow 未入库")
	}
	if f.State != flow.StateCompleted {
		t.Fatalf("终态 = %q,期望 %q", f.State, flow.StateCompleted)
	}
	if !hasTag(f.Tags, "composed") || !hasTag(f.Tags, "sse") {
		t.Fatalf("标签 = %v,应含 composed 与 sse", f.Tags)
	}
	if f.Metadata["stream"] != flow.StreamSSE {
		t.Fatalf("metadata.stream = %v,期望 %q", f.Metadata["stream"], flow.StreamSSE)
	}
	// 本机往返常常不足 1ms,DurationMs 会被截成 0,故只断言时间点确实被填过。
	if f.Timing.ResponseAt.IsZero() || f.Timing.CompletedAt.IsZero() {
		t.Fatalf("时间点未记录: responseAt=%v completedAt=%v", f.Timing.ResponseAt, f.Timing.CompletedAt)
	}
	ss, ok := app.Service.StreamSession(id)
	if !ok {
		t.Fatal("流会话未入库")
	}
	if ss.ID != f.ID {
		t.Fatalf("StreamSession.ID=%q 与 Flow.ID=%q 必须相同", ss.ID, f.ID)
	}
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// rawSSEServer 用裸 TCP 保留请求头的原始顺序、大小写和重复项，供报文断言使用。
func rawSSEServer(t *testing.T, tail func(net.Conn)) (addr string, got <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ch := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		br := bufio.NewReader(conn)
		var head strings.Builder
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			head.WriteString(line)
			if strings.TrimRight(line, "\r\n") == "" {
				break
			}
		}
		ch <- head.String()
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n"))
		_, _ = conn.Write([]byte("b\r\ndata: one\n\n\r\n"))
		tail(conn)
	}()
	return ln.Addr().String(), ch
}

func TestSendRequestSSEHeadersVerbatim(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawSSEServer(t, func(c net.Conn) { _, _ = c.Write([]byte("0\r\n\r\n")) })
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec("http://"+addr+"/events", [][2]string{
		{"Accept", "text/event-stream"},
		{"Cache-Control", "no-cache"},
		{"Last-Event-ID", "42"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	var raw string
	select {
	case raw = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
	want := strings.Join([]string{
		"GET /events HTTP/1.1",
		"Host: " + addr,
		"Accept: text/event-stream",
		"Cache-Control: no-cache",
		"Last-Event-ID: 42",
		"",
		"",
	}, "\r\n")
	if raw != want {
		t.Fatalf("线上报文与预期不符\n--- got ---\n%s\n--- want ---\n%s", raw, want)
	}
	waitFlowSettled(t, events, id)
}

// 未提供头部时 rawHeaders 为 nil，此用例覆盖标准 Transport 的增量读取。
func TestSendRequestSSEWithoutHeadersUsesFallbackTransport(t *testing.T) {
	app := newComposeApp(t)
	url, emit, done := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, nil))
	if err != nil {
		t.Fatal(err)
	}
	emit("data: one\n\n")
	got := waitStreamSession(t, app, events, id, "第 1 条消息", func(d service.StreamSessionDTOType) bool {
		return d.MessageCount >= 1
	})
	if got.Messages[0].Data != "one" {
		t.Fatalf("首条消息载荷 = %q", got.Messages[0].Data)
	}
	done()
}

// 显式 SSE 请求也可能收到 JSON 错误响应，正文读取方式取决于响应 Content-Type。
func TestSendRequestSSEFallsBackToBufferedWhenNotEventStream(t *testing.T) {
	app := newComposeApp(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	t.Cleanup(srv.Close)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(srv.URL, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatalf("非 SSE 响应不该返回错误: %v", err)
	}
	waitFlowSettled(t, events, id)

	if _, ok := app.Service.StreamSession(id); ok {
		t.Fatal("退化路径不该产生流会话")
	}
	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateCompleted {
		t.Fatalf("终态 = %q,期望 %q", f.State, flow.StateCompleted)
	}
	if string(f.Response.Body) != `{"error":"unauthorized"}` {
		t.Fatalf("响应体 = %q", string(f.Response.Body))
	}
}

// SSE 响应可能携带错误状态码，流类型由 Content-Type 决定。
func TestSendRequestSSEEventStreamWithErrorStatus(t *testing.T) {
	app := newComposeApp(t)
	url, emit, done := sseServer(t, 401, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	emit("data: nope\n\n")
	got := waitStreamSession(t, app, events, id, "第 1 条消息", func(d service.StreamSessionDTOType) bool {
		return d.MessageCount >= 1
	})
	if got.StatusCode != 401 {
		t.Fatalf("状态码 = %d,期望 401", got.StatusCode)
	}
	done()
}

// 上游截断响应时，Flow 记 errored，流会话记 closed，使界面结束等待。
func TestSendRequestSSEUpstreamError(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawSSEServer(t, func(c net.Conn) { _ = c.Close() })
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec("http://"+addr+"/events", [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q", f.State, flow.StateErrored)
	}
	if f.Error == "" {
		t.Fatal("errored 的 flow 必须带错误信息")
	}
	ss, ok := app.Service.StreamSession(id)
	if !ok || ss.Status != "closed" {
		t.Fatalf("会话 ok=%v status=%q,期望 closed", ok, ss.Status)
	}
}

func TestStopStreamEndsFlowAsCompleted(t *testing.T) {
	app := newComposeApp(t)
	url, emit, _ := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	emit("data: one\n\n")
	waitStreamSession(t, app, events, id, "第 1 条消息", func(d service.StreamSessionDTOType) bool {
		return d.MessageCount >= 1
	})

	if !app.StopStream(id) {
		t.Fatal("StopStream 未命中进行中的流")
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateCompleted {
		t.Fatalf("终态 = %q,期望 %q", f.State, flow.StateCompleted)
	}
	ss, _ := app.Service.StreamSession(id)
	if ss.Status != "closed" {
		t.Fatalf("会话状态 = %q,期望 closed", ss.Status)
	}
	if app.StopStream(id) {
		t.Fatal("已结束的流不该再被 StopStream 命中")
	}
}

// 构造器请求通过 ImportStreamSession 记录，暂停抓包录制不影响其消息时间线。
func TestSendRequestSSERecordedWhileNotRecording(t *testing.T) {
	app := newComposeApp(t)
	app.Service.StopRecording()
	url, emit, done := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	emit("data: one\n\n")
	got := waitStreamSession(t, app, events, id, "第 1 条消息", func(d service.StreamSessionDTOType) bool {
		return d.MessageCount >= 1
	})
	if got.Messages[0].Data != "one" {
		t.Fatalf("首条消息载荷 = %q", got.Messages[0].Data)
	}
	done()
}

// streamHook 记录 OnStreamMessage 调用次数,并可在第 abortAt 条上 abort。
type streamHook struct {
	calls   atomic.Int64
	abortAt int64 // 0 表示从不 abort
}

func (h *streamHook) Name() string      { return "stream" }
func (h *streamHook) Priority() int     { return 0 }
func (h *streamHook) Enabled() bool     { return true }
func (h *streamHook) Match(string) bool { return true }
func (h *streamHook) OnStreamMessage(context.Context, *flow.StreamMessage) flow.Decision {
	n := h.calls.Add(1)
	if h.abortAt > 0 && n >= h.abortAt {
		return flow.AbortDecision(0, "测试中止")
	}
	return flow.ContinueDecision()
}

func TestSendRequestSSEViaPipelineGatesStreamHooks(t *testing.T) {
	t.Run("关", func(t *testing.T) {
		app := newComposeApp(t)
		hook := &streamHook{}
		app.Pipeline.RegisterCore(hook)
		url, emit, done := sseServer(t, 200, "text/event-stream")
		events, unsubscribe := app.Engine.Bus().Subscribe()
		t.Cleanup(unsubscribe)

		id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
		if err != nil {
			t.Fatal(err)
		}
		emit("data: one\n\n")
		waitStreamSession(t, app, events, id, "第 1 条消息", func(d service.StreamSessionDTOType) bool {
			return d.MessageCount >= 1
		})
		done()
		waitFlowSettled(t, events, id)
		if got := hook.calls.Load(); got != 0 {
			t.Fatalf("ViaPipeline 关时流钩子被调用了 %d 次", got)
		}
	})

	t.Run("开", func(t *testing.T) {
		app := newComposeApp(t)
		hook := &streamHook{}
		app.Pipeline.RegisterCore(hook)
		url, emit, done := sseServer(t, 200, "text/event-stream")
		events, unsubscribe := app.Engine.Bus().Subscribe()
		t.Cleanup(unsubscribe)

		spec := sseSpec(url, [][2]string{{"Accept", "text/event-stream"}})
		spec.ViaPipeline = true
		id, err := app.SendRequest(spec)
		if err != nil {
			t.Fatal(err)
		}
		emit("data: one\n\ndata: two\n\n")
		waitStreamSession(t, app, events, id, "第 2 条消息", func(d service.StreamSessionDTOType) bool {
			return d.MessageCount >= 2
		})
		done()
		waitFlowSettled(t, events, id)
		if got := hook.calls.Load(); got != 2 {
			t.Fatalf("流钩子调用 %d 次,期望 2", got)
		}
	})

	t.Run("Abort 终止流", func(t *testing.T) {
		app := newComposeApp(t)
		hook := &streamHook{abortAt: 1}
		app.Pipeline.RegisterCore(hook)
		url, emit, _ := sseServer(t, 200, "text/event-stream")
		events, unsubscribe := app.Engine.Bus().Subscribe()
		t.Cleanup(unsubscribe)

		spec := sseSpec(url, [][2]string{{"Accept", "text/event-stream"}})
		spec.ViaPipeline = true
		id, err := app.SendRequest(spec)
		if err != nil {
			t.Fatal(err)
		}
		emit("data: one\n\n")
		waitFlowSettled(t, events, id)

		f, _ := app.Service.RawFlow(id)
		if f.State != flow.StateBlocked {
			t.Fatalf("被插件中止的流终态 = %q,期望 %q", f.State, flow.StateBlocked)
		}
		ss, ok := app.Service.StreamSession(id)
		if !ok || ss.Status != "closed" {
			t.Fatalf("会话 ok=%v status=%q,期望 closed", ok, ss.Status)
		}
		if ss.MessageCount != 0 {
			t.Fatalf("被 abort 的事件不该入库,实际 %d 条", ss.MessageCount)
		}
	})
}
