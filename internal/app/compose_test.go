// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

func newComposeApp(t *testing.T) *App {
	t.Helper()
	root, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := core.NewEngine(DefaultConfig(), core.WithCA(root))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(root, engine.Bus(), "", "")
	return &App{Engine: engine, Service: svc, Pipeline: pipeline.New(nil, nil), CertDir: t.TempDir()}
}

// rawEchoServer 用裸 TCP 收一次请求并把原始报文交回:走 net/http 的话头部会被折进
// map,顺序与大小写都没了,而那正是构造器要保证的东西。
func rawEchoServer(t *testing.T) (addr string, got <-chan string) {
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
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		br := bufio.NewReader(conn)
		var head strings.Builder
		contentLength := 0
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			head.WriteString(line)
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				break
			}
			if name, value, ok := strings.Cut(trimmed, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
				for _, c := range strings.TrimSpace(value) {
					if c >= '0' && c <= '9' {
						contentLength = contentLength*10 + int(c-'0')
					}
				}
			}
		}
		body := make([]byte, contentLength)
		if contentLength > 0 {
			if _, err := readFull(br, body); err != nil {
				return
			}
		}
		ch <- head.String() + string(body)
		_, _ = conn.Write([]byte("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n"))
	}()
	return ln.Addr().String(), ch
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// SendRequest 的核心承诺:用户键入什么就发什么。这里一次性钉住顺序、大小写、
// 重复项、逐跳头剔除与 Content-Length 重算。
func TestSendRequestWritesHeadersVerbatim(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	id, err := app.SendRequest(flow.RequestSpec{
		Method: "post",
		URL:    "http://" + addr + "/v1/charges?limit=20",
		Headers: [][2]string{
			{"content-type", "application/json"},
			{"X-Trace-Id", "abc-123"},
			{"Accept", "application/json"},
			{"Accept", "text/plain"},
			{"Connection", "keep-alive"},
			{"Content-Encoding", "gzip"},
			{"Content-Length", "999"},
		},
		Body: `{"amount":2000}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("SendRequest 未返回 flow id")
	}

	var raw string
	select {
	case raw = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}

	want := strings.Join([]string{
		"POST /v1/charges?limit=20 HTTP/1.1",
		"Host: " + addr,
		"content-type: application/json",
		"X-Trace-Id: abc-123",
		"Accept: application/json",
		"Accept: text/plain",
		"Content-Length: 15",
		"",
		`{"amount":2000}`,
	}, "\r\n")
	if raw != want {
		t.Fatalf("线上报文与预期不符\n--- got ---\n%s\n--- want ---\n%s", raw, want)
	}
}

// Content-Length 与 TE 的值由出站侧重算,但位置留在用户写它的那一行上:
// ApplyRequestToHTTP 先 Del/Set 进头表,再由 reconcileOrderedHeaders 沿 RawHeaders 的原顺序
// 回填,于是结果是「第一条同名行拿到新值、其余同名行消失」。
//
// 构造器的线缆预览(web/src/workbench/views/compose/wire.ts 的 buildWire)是这段行为的
// 镜像实现,靠注释约定同步。这条用例把参考行为钉死:改了这里就得同步改那边,
// 否则预览与实发会在这两个头上错位——而预览会说谎正是这个功能最不能出的问题。
func TestSendRequestKeepsRecomputedHeadersInPlace(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	if _, err := app.SendRequest(flow.RequestSpec{
		Method: "POST",
		URL:    "http://" + addr + "/",
		Headers: [][2]string{
			{"content-length", "999"}, // 值被重算成 5,位置与大小写不动
			{"TE", "gzip"},            // 首条 TE 收下 "trailers"
			{"X-A", "1"},
			{"te", "trailers"},      // 第二条 TE 消失
			{"Content-Length", "7"}, // 第二条 Content-Length 同样消失
		},
		Body: "hello",
	}); err != nil {
		t.Fatal(err)
	}

	var raw string
	select {
	case raw = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}

	want := strings.Join([]string{
		"POST / HTTP/1.1",
		"Host: " + addr,
		"content-length: 5",
		"TE: trailers",
		"X-A: 1",
		"",
		"hello",
	}, "\r\n")
	if raw != want {
		t.Fatalf("重算头的位置与预期不符\n--- got ---\n%s\n--- want ---\n%s", raw, want)
	}
}

// 用户没写 User-Agent 就不该有 User-Agent:net/http 默认会注入 Go-http-client/1.1,
// 那会让「所见即所发」当场失效。
func TestSendRequestDoesNotInjectUserAgent(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	if _, err := app.SendRequest(flow.RequestSpec{
		Method:  "GET",
		URL:     "http://" + addr + "/",
		Headers: [][2]string{{"Accept", "*/*"}},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case raw := <-got:
		if strings.Contains(strings.ToLower(raw), "user-agent") {
			t.Fatalf("出站请求被注入了 User-Agent:\n%s", raw)
		}
		if strings.Contains(raw, "Content-Length") {
			t.Fatalf("无体的 GET 不该带 Content-Length:\n%s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
}

// 一条头都不写才是真正的零头部场景:splitComposedHeaders 此时返回 RawHeaders=nil,
// 而阻止 net/http 注入 UA 的空值哨兵一度挂在 len(RawHeaders)>0 里,根本设不上。
// 上面那个用例带了 Accept,恰好绕开了这条路径。
func TestSendRequestWithNoHeadersDoesNotInjectUserAgent(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	if _, err := app.SendRequest(flow.RequestSpec{Method: "GET", URL: "http://" + addr + "/probe"}); err != nil {
		t.Fatal(err)
	}

	select {
	case raw := <-got:
		if strings.Contains(strings.ToLower(raw), "user-agent") {
			t.Fatalf("零头部请求被注入了 User-Agent:\n%s", raw)
		}
		want := "GET /probe HTTP/1.1\r\nHost: " + addr + "\r\n\r\n"
		if raw != want {
			t.Fatalf("线上报文与预期不符\n--- got ---\n%q\n--- want ---\n%q", raw, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
}

// 哨兵的判据是「最终出站头里有没有 UA」而不是「RawHeaders 里有没有」。
// ResendFlow 同样不带 RawHeaders,但 Header map 里存着抓到的真实 UA —— 无条件设哨兵会把它抹掉。
func TestResendKeepsCapturedUserAgentWithoutRawHeaders(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	orig := flow.New(flow.ProtoHTTP)
	orig.Request = &flow.Request{
		Method: "GET",
		URL:    "http://" + addr + "/replay",
		Host:   addr,
		Path:   "/replay",
		Proto:  "HTTP/1.1",
		Header: map[string][]string{"User-Agent": {"curl/8.4.0"}},
	}
	app.Service.ImportFlowCompleted(orig)

	if !app.ResendFlow(orig.ID) {
		t.Fatal("ResendFlow 未找到蓝本")
	}
	select {
	case raw := <-got:
		if !strings.Contains(raw, "User-Agent: curl/8.4.0") {
			t.Fatalf("重发丢掉了抓到的 User-Agent:\n%s", raw)
		}
		if strings.Contains(raw, "Go-http-client") {
			t.Fatalf("重发不该用 Go 的默认 UA 覆盖:\n%s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
}

// 体过大直接在 app 边界拒收:桌面 Bridge 直连 SendRequest,REST 的请求体上限管不到它。
func TestSendRequestRejectsOversizedBody(t *testing.T) {
	app := newComposeApp(t)
	_, err := app.SendRequest(flow.RequestSpec{
		Method: "POST",
		URL:    "http://127.0.0.1:1/",
		Body:   strings.Repeat("x", flow.MaxComposeBodyBytes+1),
	})
	if err == nil || !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("超限的请求体应被拒绝,实际 err=%v", err)
	}
	// 边界值本身要放行(这里连不上是预期的,只要不是被上限拦下)。
	if _, err := app.SendRequest(flow.RequestSpec{
		Method: "POST",
		URL:    "http://127.0.0.1:1/",
		Body:   strings.Repeat("x", flow.MaxComposeBodyBytes),
	}); err != nil && strings.Contains(err.Error(), "超过上限") {
		t.Fatal("恰好等于上限的请求体不该被拒绝")
	}
}

func TestSendRequestRejectsBadInput(t *testing.T) {
	app := newComposeApp(t)
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"空 URL", "   ", "URL 为空"},
		{"不支持的协议", "ftp://example.com/x", "不支持的协议"},
		{"无法解析", "http://exa mple.com/x", "无法解析"},
		{"缺少主机名", "http:///onlypath", "缺少主机名"},
		{"WebSocket 协议", "ws://a/b", "OpenWebSocket"},
		{"WebSocket 加密协议", "wss://a/b", "OpenWebSocket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := app.SendRequest(flow.RequestSpec{Method: "GET", URL: tc.url})
			if err == nil {
				t.Fatalf("期望报错,却返回了 flow id %q", id)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误信息 %q 不含 %q", err.Error(), tc.want)
			}
			if id != "" {
				t.Fatalf("失败时不该产生 flow,却返回了 %q", id)
			}
		})
	}
}

// 裸 host/path 补 https 而非 http:猜错时握手立刻失败可见,反过来会把本该加密的请求明文发出去。
func TestSendRequestDefaultsToHTTPS(t *testing.T) {
	app := newComposeApp(t)
	id, err := app.SendRequest(flow.RequestSpec{Method: "GET", URL: "example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	f, ok := app.Service.RawFlow(id)
	if !ok {
		t.Fatal("新 flow 未入库")
	}
	if f.Protocol != flow.ProtoHTTPS {
		t.Fatalf("协议 = %q,期望 %q", f.Protocol, flow.ProtoHTTPS)
	}
	if f.Request.URL != "https://example.com/v1" {
		t.Fatalf("URL = %q", f.Request.URL)
	}
	if f.Request.Method != "GET" {
		t.Fatalf("方法 = %q", f.Request.Method)
	}
}

// 钩子跑在 runResend 的后台 goroutine 上,断言在测试 goroutine 上,故计数器必须原子化。
type recordingHook struct {
	requests  atomic.Int64
	responses atomic.Int64
}

func (h *recordingHook) Name() string      { return "recording" }
func (h *recordingHook) Priority() int     { return 0 }
func (h *recordingHook) Enabled() bool     { return true }
func (h *recordingHook) Match(string) bool { return true }
func (h *recordingHook) OnRequest(context.Context, *flow.Flow) flow.Decision {
	h.requests.Add(1)
	return flow.ContinueDecision()
}
func (h *recordingHook) OnResponse(context.Context, *flow.Flow) flow.Decision {
	h.responses.Add(1)
	return flow.ContinueDecision()
}

// waitFlowCompleted 等到 id 对应的 flow 收尾(finishResend 经 ImportFlowCompleted 广播
// flow_updated)。订阅必须早于 SendRequest:总线对慢订阅者丢消息,不补发历史事件。
func waitFlowCompleted(t *testing.T, ch <-chan core.Event, id string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != core.EventFlowUpdated {
				continue
			}
			if dto, ok := ev.Payload.(service.HTTPSessionDTO); ok && dto.ID == id {
				return
			}
		case <-deadline:
			t.Fatal("等待 flow 收尾超时")
		}
	}
}

// ViaPipeline 是构造器「所见即所发」的开关:关掉时规则/插件/断点一律不介入。
func TestSendRequestViaPipelineGatesHooks(t *testing.T) {
	for _, viaPipeline := range []bool{false, true} {
		t.Run(map[bool]string{false: "关", true: "开"}[viaPipeline], func(t *testing.T) {
			app := newComposeApp(t)
			hook := &recordingHook{}
			app.Pipeline.RegisterCore(hook)
			addr, got := rawEchoServer(t)

			events, unsubscribe := app.Engine.Bus().Subscribe()
			t.Cleanup(unsubscribe)

			id, err := app.SendRequest(flow.RequestSpec{
				Method:      "GET",
				URL:         "http://" + addr + "/",
				Headers:     [][2]string{{"Accept", "*/*"}},
				ViaPipeline: viaPipeline,
			})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-got:
			case <-time.After(5 * time.Second):
				t.Fatal("上游未收到请求")
			}
			// 响应钩子在收尾广播之前跑完,等到广播即可安全读计数。
			waitFlowCompleted(t, events, id)

			wantCalls := int64(0)
			if viaPipeline {
				wantCalls = 1
			}
			if hook.requests.Load() != wantCalls || hook.responses.Load() != wantCalls {
				t.Fatalf("钩子调用 请求=%d 响应=%d,期望各 %d", hook.requests.Load(), hook.responses.Load(), wantCalls)
			}
		})
	}
}

func TestSplitComposedHeaders(t *testing.T) {
	t.Run("按 URL 补 Host 并置于首位", func(t *testing.T) {
		host, header, raw := splitComposedHeaders([][2]string{{"Accept", "*/*"}}, "example.com")
		if host != "example.com" {
			t.Fatalf("host = %q", host)
		}
		if len(raw) != 2 || raw[0] != [2]string{"Host", "example.com"} {
			t.Fatalf("原始头序列 = %v", raw)
		}
		if _, ok := header["Host"]; ok {
			t.Fatal("Host 不该进规范化 map:net/http 从 req.Host 读它")
		}
	})

	t.Run("用户写的 Host 覆盖 URL 且保持原位", func(t *testing.T) {
		host, _, raw := splitComposedHeaders([][2]string{{"Accept", "*/*"}, {"host", "override.test"}}, "example.com")
		if host != "override.test" {
			t.Fatalf("host = %q", host)
		}
		if len(raw) != 2 || raw[1][0] != "host" {
			t.Fatalf("原始头序列 = %v", raw)
		}
	})

	t.Run("重复头合进同名切片", func(t *testing.T) {
		_, header, raw := splitComposedHeaders([][2]string{{"accept", "a"}, {"Accept", "b"}}, "example.com")
		if got := header["Accept"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("Accept = %v", got)
		}
		if len(raw) != 3 {
			t.Fatalf("原始头序列应含 Host + 两条 Accept,实际 %v", raw)
		}
	})

	t.Run("空名字的行被丢弃", func(t *testing.T) {
		_, header, raw := splitComposedHeaders([][2]string{{"  ", "orphan"}, {"Accept", "*/*"}}, "example.com")
		if len(header) != 1 {
			t.Fatalf("规范化 map = %v", header)
		}
		if len(raw) != 2 {
			t.Fatalf("原始头序列 = %v", raw)
		}
	})

	t.Run("SSE 保留 Accept 与 Cache-Control", func(t *testing.T) {
		_, header, raw := splitComposedHeaders([][2]string{
			{"Accept", "text/event-stream"},
			{"Cache-Control", "no-cache"},
			{"Last-Event-ID", "42"},
		}, "example.com")
		if got := header["Accept"]; len(got) != 1 || got[0] != "text/event-stream" {
			t.Fatalf("Accept = %v", got)
		}
		if got := header["Cache-Control"]; len(got) != 1 || got[0] != "no-cache" {
			t.Fatalf("Cache-Control = %v", got)
		}
		if got := header["Last-Event-Id"]; len(got) != 1 || got[0] != "42" {
			t.Fatalf("Last-Event-ID = %v", got)
		}
		want := [][2]string{{"Host", "example.com"}, {"Accept", "text/event-stream"}, {"Cache-Control", "no-cache"}, {"Last-Event-ID", "42"}}
		if len(raw) != len(want) {
			t.Fatalf("原始头序列 = %v", raw)
		}
		for i := range want {
			if raw[i] != want[i] {
				t.Fatalf("原始头序列第 %d 项 = %v,期望 %v", i, raw[i], want[i])
			}
		}
	})

	// 上游 Transport DisableCompression,凭空多出的 Accept-Encoding 会让 SSE 变成压缩流,
	// 增量解析要多绕一层。用户没写就不该出现。
	t.Run("不注入 Accept-Encoding", func(t *testing.T) {
		_, header, raw := splitComposedHeaders([][2]string{{"Accept", "text/event-stream"}}, "example.com")
		if _, ok := header["Accept-Encoding"]; ok {
			t.Fatalf("规范化 map 里凭空出现了 Accept-Encoding: %v", header)
		}
		for _, kv := range raw {
			if strings.EqualFold(kv[0], "Accept-Encoding") {
				t.Fatalf("原始头序列里凭空出现了 Accept-Encoding: %v", raw)
			}
		}
	})

	t.Run("一条头都没有时不进保真写线路径", func(t *testing.T) {
		host, header, raw := splitComposedHeaders(nil, "example.com")
		if host != "example.com" || len(header) != 0 || raw != nil {
			t.Fatalf("host=%q header=%v raw=%v", host, header, raw)
		}
	})
}

// GraphQL 在后端与 HTTP 完全同路:body 由前端合成好,这里只多打一个标签。
func TestSendRequestGraphQLTagsFlow(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	const body = `{"query":"query Q($id:ID!){ node(id:$id){ id } }","variables":{"id":"1"}}`
	id, err := app.SendRequest(flow.RequestSpec{
		Kind:    flow.SpecKindGraphQL,
		Method:  "POST",
		URL:     "http://" + addr + "/graphql",
		Headers: [][2]string{{"Content-Type", "application/json"}},
		Body:    body,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-got:
		if !strings.HasSuffix(raw, body) {
			t.Fatalf("请求体与 spec.Body 不一致:\n%s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
	waitFlowCompleted(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if !hasTag(f.Tags, "graphql") {
		t.Fatalf("标签 = %v,应含 graphql", f.Tags)
	}
	if _, ok := app.Service.StreamSession(id); ok {
		t.Fatal("GraphQL 走的是缓冲往返,不该产生流会话")
	}
	if f.State != flow.StateCompleted {
		t.Fatalf("终态 = %q", f.State)
	}
}
