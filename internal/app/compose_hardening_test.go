// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// 构造器的头是原样写线的,含 CR/LF 就能拼出额外的头乃至第二个请求。
// 入口就该拒绝,并且不留下任何 flow —— 与「URL 无法解析」的处理一致。
func TestSendRequestRejectsCRLFHeader(t *testing.T) {
	app := newComposeApp(t)
	for _, c := range []struct {
		name    string
		headers [][2]string
	}{
		{"值里的 CRLF", [][2]string{{"X-A", "1\r\nX-Injected: 1"}}},
		{"名里的 CRLF", [][2]string{{"X-A\r\nX-Injected", "1"}}},
		{"值里的 NUL", [][2]string{{"X-A", "1\x00"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, err := app.SendRequest(flow.RequestSpec{Method: "GET", URL: "http://example.test/p", Headers: c.headers})
			if err == nil {
				t.Fatal("含 CR/LF/NUL 的头应被拒绝")
			}
			if id != "" {
				t.Fatalf("被拒的请求不该留下 flow,得到 id=%q", id)
			}
		})
	}
}

// 上游客户端不得代替客户端跟随 30x:代跟随会让那一跳从抓包里消失,
// 还会经保真路径把上一跳的凭据与旧 Host 重放给新主机。
func TestComposeDoesNotFollowRedirects(t *testing.T) {
	hit := make(chan struct{}, 4)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/next", http.StatusFound)
	}))
	defer origin.Close()

	app := newComposeApp(t)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(flow.RequestSpec{
		Method:  "GET",
		URL:     origin.URL + "/start",
		Headers: [][2]string{{"Authorization", "Bearer SECRET"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFlowCompleted(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.Response == nil || f.Response.Status != http.StatusFound {
		t.Fatalf("构造器应原样记下 302,得到 %+v", f.Response)
	}
	select {
	case <-hit:
		t.Fatal("重定向被自动跟随了:那一跳既不可见,又会把凭据带去新主机")
	default:
	}
}

// gorilla 对附加头里的 Host 有专门分支(赋给 req.Host):用户写的 Host 必须透传到上游,
// 否则虚拟主机 / 按 Host 签名的场景连不通,线缆预览也与实发对不上。
func TestComposeWSCustomHostReachesUpstream(t *testing.T) {
	gotHost := make(chan string, 4)
	up := gws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost <- r.Host
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				_ = c.Close()
				return
			}
		}
	}))
	defer srv.Close()

	app := newComposeApp(t)
	t.Cleanup(app.CloseAllWebSockets)
	id, err := app.OpenWebSocket(flow.RequestSpec{
		URL:     "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/ws",
		Headers: [][2]string{{"Host", "virtual.example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h := <-gotHost; h != "virtual.example.com" {
		t.Fatalf("上游看到的 Host = %q,期望构造器里写的 virtual.example.com", h)
	}
	_ = app.CloseWebSocket(id)
}

// 构造器窗口关闭时由 Go 侧兜底收流:SSE 既无读超时也无总超时,漏一条就挂到进程退出。
func TestStopAllStreamsEndsActiveStreams(t *testing.T) {
	app := newComposeApp(t)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	// 每条流各用一个服务端:共用的话 emit 只会被其中一个 handler 收走。
	// 停之前先等到首条消息,否则停的可能是一条请求都还没发出的流,测不到「进行中被收掉」。
	ids := make([]string, 0, 2)
	pending := make(map[string]bool, 2)
	for i := range 2 {
		url, emit, _ := sseServer(t, 200, "text/event-stream")
		id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
		if err != nil {
			t.Fatal(err)
		}
		emit("data: one\n\n")
		waitStreamSession(t, app, events, id, fmt.Sprintf("第 %d 条流的首条消息", i+1), func(d service.StreamSessionDTOType) bool {
			return d.MessageCount >= 1
		})
		ids = append(ids, id)
		pending[id] = true
	}

	app.StopAllStreams()
	// 两条流的收尾事件顺序不定,必须在同一个循环里认领 —— 逐条 waitFlowSettled
	// 会把另一条的事件当成不匹配丢掉,而总线不补发。
	deadline := time.After(5 * time.Second)
	for len(pending) > 0 {
		select {
		case ev := <-events:
			if ev.Type != core.EventFlowUpdated {
				continue
			}
			if dto, ok := ev.Payload.(service.HTTPSessionDTO); ok && pending[dto.ID] && dto.Status != "pending" {
				delete(pending, dto.ID)
			}
		case <-deadline:
			t.Fatalf("这些流没有收尾: %v", pending)
		}
	}
	for _, id := range ids {
		if app.StopStream(id) {
			t.Fatalf("StopAllStreams 之后 %s 仍在注册表里", id)
		}
	}
}

// 上限与 WS 侧的 maxComposeWS 同理:窗口可能在不通知 Go 的情况下被销毁,
// 没有封顶就只剩进程退出这一个收口点。超限时不该留下任何 flow。
func TestComposeStreamCapRejectsExcess(t *testing.T) {
	app := newComposeApp(t)
	t.Cleanup(app.StopAllStreams)
	url, _, _ := sseServer(t, 200, "text/event-stream")

	for i := range maxComposeStreams {
		if _, err := app.SendRequest(sseSpec(url, nil)); err != nil {
			t.Fatalf("第 %d 条流不该被拒: %v", i+1, err)
		}
	}
	id, err := app.SendRequest(sseSpec(url, nil))
	if err == nil {
		t.Fatal("超出上限的流应被拒绝")
	}
	if id != "" {
		t.Fatalf("被拒的流不该留下 flow,得到 id=%q", id)
	}
}
