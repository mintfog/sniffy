// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

func TestSendRequestDetectsSSE(t *testing.T) {
	for _, tc := range []struct {
		name        string
		kind        string
		compressed  bool
		viaPipeline bool
	}{
		{name: "默认 HTTP"},
		{name: "HTTP 压缩响应", kind: flow.SpecKindHTTP, compressed: true},
		{name: "GraphQL 经过管道", kind: flow.SpecKindGraphQL, viaPipeline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newComposeApp(t)
			hook := &streamHook{}
			app.Pipeline.RegisterCore(hook)
			const body = `{"prompt":"生成脚本"}`
			requests := make(chan flow.RequestSpec, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				requests <- flow.RequestSpec{Method: r.Method, Body: string(data), Headers: [][2]string{
					{"Accept", r.Header.Get("Accept")}, {"Content-Type", r.Header.Get("Content-Type")},
				}}
				w.Header().Set("Content-Type", "Text/Event-Stream; charset=utf-8")
				var writer io.Writer = w
				if tc.compressed {
					w.Header().Set("Content-Encoding", "gzip")
					zw := gzip.NewWriter(w)
					defer zw.Close()
					writer = zw
				}
				_, _ = io.WriteString(writer, "event: progress\ndata: 收到请求\n\n")
				if zw, ok := writer.(*gzip.Writer); ok {
					_ = zw.Flush()
				}
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(app.StopAllStreams)
			events, unsubscribe := app.Engine.Bus().Subscribe()
			t.Cleanup(unsubscribe)

			id, err := app.SendRequest(flow.RequestSpec{
				Kind: tc.kind, Method: "POST", URL: srv.URL, Body: body,
				Headers:     [][2]string{{"Accept", "*/*"}, {"Content-Type", "application/json"}, {"Accept-Encoding", "gzip, deflate, br"}},
				ViaPipeline: tc.viaPipeline,
			})
			if err != nil {
				t.Fatal(err)
			}
			ss := waitStreamSession(t, app, events, id, "自动识别并读取首个事件", func(s service.StreamSessionDTOType) bool {
				return s.MessageCount == 1
			})
			if ss.Status != "open" || ss.Messages[0].EventType != "progress" || ss.Messages[0].Data != "收到请求" {
				t.Fatalf("实时事件 = %+v", ss)
			}
			request := <-requests
			if request.Method != "POST" || request.Body != body || request.Headers[0][1] != "*/*" || request.Headers[1][1] != "application/json" {
				t.Fatalf("上游收到的请求 = %+v", request)
			}
			f, _ := app.Service.RawFlow(id)
			if f.State != flow.StateAwaitingResponse || len(f.Response.Body) != 0 || f.Metadata["stream"] != flow.StreamSSE {
				t.Fatalf("流进行中的 HTTP 快照 = %+v", f)
			}
			if !app.StopStream(id) {
				t.Fatal("自动识别的流未能停止")
			}
			final := waitFlowSettled(t, events, id)
			if final.Status != "completed" {
				t.Fatalf("停止后的状态 = %q，期望 completed", final.Status)
			}
			wantCalls := int64(0)
			if tc.viaPipeline {
				wantCalls = 1
			}
			if got := hook.calls.Load(); got != wantCalls {
				t.Fatalf("流钩子调用次数 = %d，期望 %d", got, wantCalls)
			}
		})
	}
}

func TestSendRequestKeepsOrdinaryResponseBuffered(t *testing.T) {
	app := newComposeApp(t)
	const body = "event: progress\ndata: 正文示例\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	events, unsubscribe := app.Engine.Bus().Subscribe()
	defer unsubscribe()
	id, err := app.SendRequest(flow.RequestSpec{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)
	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateCompleted || string(f.Response.Body) != body {
		t.Fatalf("普通响应 = %+v", f)
	}
	if _, ok := app.Service.StreamSession(id); ok {
		t.Fatal("text/plain 响应被识别为 SSE")
	}
}

func TestStopComposeRequestBeforeResponseHeaders(t *testing.T) {
	app := newComposeApp(t)
	started := make(chan struct{})
	stopped := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(app.StopAllStreams)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)
	id, err := app.SendRequest(flow.RequestSpec{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("请求未到达上游")
	}
	if !app.StopStream(id) {
		t.Fatal("等待响应头的请求未能取消")
	}
	waitFlowSettled(t, events, id)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("取消请求未关闭上游连接")
	}
}

func TestDetectedSSEHonorsStreamLimit(t *testing.T) {
	app := newComposeApp(t)
	t.Cleanup(app.StopAllStreams)
	// 占满流名额，普通响应仍须可发送，自动识别出的额外流须终止。
	for i := range maxComposeStreams {
		id := string(rune('a' + i))
		_, cancel := context.WithCancel(t.Context())
		app.outStreams.add(id, cancel)
		if err := app.outStreams.reserveStream(id); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		_, _ = io.WriteString(w, "data: hello\n\n")
	}))
	defer srv.Close()
	events, unsubscribe := app.Engine.Bus().Subscribe()
	defer unsubscribe()

	id, err := app.SendRequest(flow.RequestSpec{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitFlowSettled(t, events, id); got.Status != "completed" {
		t.Fatalf("普通请求状态 = %q，期望 completed", got.Status)
	}
	id, err = app.SendRequest(flow.RequestSpec{URL: srv.URL + "/events"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitFlowSettled(t, events, id); got.Status != "error" || !strings.Contains(got.Error, "出站流数量已达上限") {
		t.Fatalf("超额流状态 = %+v", got)
	}
}

func TestComposeHTTPResponseHonorsTimeout(t *testing.T) {
	app := newComposeApp(t)
	app.Engine.UpstreamClient().Timeout = 150 * time.Millisecond
	url, emit, _ := sseServer(t, 200, "application/json")
	t.Cleanup(app.StopAllStreams)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(flow.RequestSpec{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	emit("data: one\n\n")
	got := waitFlowSettled(t, events, id)
	if got.Status != "error" || !strings.Contains(got.Error, "超时") {
		t.Fatalf("普通响应超时状态 = %+v", got)
	}
}

func TestDetectedSSEClearsRequestTimeout(t *testing.T) {
	app := newComposeApp(t)
	app.Engine.UpstreamClient().Timeout = 150 * time.Millisecond
	url, emit, done := sseServer(t, 200, "text/event-stream")
	t.Cleanup(app.StopAllStreams)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(flow.RequestSpec{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	emit("data: one\n\n")
	waitStreamSession(t, app, events, id, "首条事件", func(s service.StreamSessionDTOType) bool {
		return s.MessageCount == 1
	})
	// 跨过普通请求的总超时后仍能接收事件，才证明自动识别时撤销了计时器。
	<-time.After(2 * app.Engine.UpstreamClient().Timeout)
	emit("data: two\n\n")
	waitStreamSession(t, app, events, id, "总超时之后的事件", func(s service.StreamSessionDTOType) bool {
		return s.MessageCount == 2
	})
	done()
	waitFlowSettled(t, events, id)
}
