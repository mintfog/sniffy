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
	"github.com/mintfog/sniffy/internal/service"
)

// sseServer 起一个由测试逐块放行的流式服务端:emit 写一块并 flush,done 结束响应。
// 「逐块放行」是这组测试的关键——服务端还挂着时就能断言已记录的消息,才证明是增量而非读完才记。
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
	// Cleanup 是后进先出:先结束响应,srv.Close 才不会卡在等待未完成的请求上。
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

// waitStreamSession 等一条满足 want 的流会话快照(经事件总线,不 sleep 轮询)。
// 订阅必须早于 SendRequest:总线对慢订阅者丢消息,不补发历史事件。
//
// 推送只带增量(service.StreamDeltaDTO),整条会话改从 store 取:那是权威副本,
// 顺带核对「事件已发出 → 存储已更新」这条顺序。增量本身的合并语义另测,见 TestStreamDeltaCarriesOnlyNewMessage。
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

// waitFlowSettled 等一条 flow 走到终态。不能复用 waitFlowCompleted:SSE 路径在收到响应头时
// 也会广播一次 flow_updated(让 UI 立刻看到状态码),那次的 flow 还停在 awaiting_response。
func waitFlowSettled(t *testing.T, ch <-chan core.Event, id string) service.HTTPSessionDTO {
	t.Helper()
	// 放宽到 30s 是为了 -race:单事件上限那个用例要逐字节扫过 8 MiB,竞态检测器下要跑六秒多。
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

func TestSendRequestSSERecordsStreamSessionIncrementally(t *testing.T) {
	app := newComposeApp(t)
	url, emit, done := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}

	emit("data: one\n\n")
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
	if first.Messages[0].Data != "one" {
		t.Fatalf("首条消息载荷 = %q,期望 %q", first.Messages[0].Data, "one")
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

// 流式响应体不该进 Flow.Body,且流还在跑时 flow 不能提前变 completed
// ——否则构造器窗口会渲染成绿色「已完成」。
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

// rawSSEServer 用裸 TCP 收一次请求交回原始报文,再按 SSE 应答一条事件后关闭。
// 走 net/http 的话头部会被折进 map,顺序与大小写都没了,而那正是构造器要保证的东西。
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

// 不给任何 header 时 rawHeaders 为 nil,走标准 Transport 而非保真写线路径:两条传输栈都要能增量记录。
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

// 401 鉴权失败页一类是常见场景:不是 SSE 就退化成一次缓冲往返,而不是报错。
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

// 状态码不是 200 但 Content-Type 是 SSE:仍按流处理(不少网关的错误流就长这样)。
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

// 上游中途断开:flow 记 errored,同时会话必须被置 closed —— 悬挂的 open 会让 UI 一直转圈。
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

// 用户主动停止是这条流的正常终点,不是错误。
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

// 暂停录制只该管抓来的流量,用户亲手点的请求仍要记录(ImportStreamSession 旁路)。
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
