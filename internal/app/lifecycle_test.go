// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

func appScenarioProcess(t *testing.T) bool {
	t.Helper()
	if inAppTestSubprocess(t) {
		isolateAppDirs(t)
		preserveAppLogging(t)
		return true
	}
	if out, err := appTestSubprocess(t); err != nil {
		t.Fatalf("场景子进程失败: %v\n%s", err, out)
	}
	return false
}

func buildRunningApp(t *testing.T) *App {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Address, cfg.Port = "127.0.0.1", 0
	a, err := Build(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	return a
}

func awaitAppSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("等待%s超时", what)
	}
}

func TestStopActiveStreams(t *testing.T) {
	if !appScenarioProcess(t) {
		return
	}
	sseClosed, wsClosed := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sse" {
			defer close(sseClosed)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		c, err := (&gws.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer close(wsClosed)
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(upstream.Close)
	a := buildRunningApp(t)
	events, unsub := a.Engine.Bus().Subscribe()
	defer unsub()
	id, err := a.SendRequest(sseSpec(upstream.URL+"/sse", nil))
	if err != nil {
		t.Fatal(err)
	}
	waitStreamSession(t, a, events, id, "流已建立", func(d service.StreamSessionDTOType) bool { return d.MessageCount == 1 })
	wsID, err := a.OpenWebSocket(flow.RequestSpec{URL: "ws" + strings.TrimPrefix(upstream.URL, "http") + "/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	awaitAppSignal(t, sseClosed, "SSE 对端断开")
	awaitAppSignal(t, wsClosed, "WebSocket 对端断开")
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, fok := a.Service.Session(id)
		s, sok := a.Service.StreamSession(id)
		w, wok := a.Service.WSSession(wsID)
		a.outStreams.mu.Lock()
		streams := len(a.outStreams.items)
		a.outStreams.mu.Unlock()
		a.outWS.mu.Lock()
		sockets := len(a.outWS.conns) + len(a.outWS.dials)
		a.outWS.mu.Unlock()
		if fok && sok && wok && f.Status == "completed" && s.Status == "closed" && w.Status == "closed" && w.EndTime != "" && streams == 0 && sockets == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("停止后未收尾: flow=%+v stream=%+v ws=%+v 注册表=%d/%d", f, s, w, streams, sockets)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAutomaticWebSocketHeartbeat(t *testing.T) {
	if !appScenarioProcess(t) {
		return
	}
	composeWSPingPeriod = 20 * time.Millisecond
	a := newComposeApp(t)
	srv := newWSTestServer(t, false)
	id, err := a.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	defer a.CloseWebSocket(id)
	for range 2 {
		if f := srv.nextFrame(t, srv.control, "定时 ping"); f.mt != gws.PingMessage || len(f.data) != 0 {
			t.Fatalf("心跳帧异常: %+v", f)
		}
	}
	if s, _ := a.Service.WSSession(id); s.MessageCount != 0 {
		t.Fatalf("心跳进入消息记录: %+v", s)
	}
}

func TestAutomaticWebSocketHeartbeatWriteFailure(t *testing.T) {
	if !appScenarioProcess(t) {
		return
	}
	composeWSPingPeriod = 20 * time.Millisecond
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&gws.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		// 保留对端写半边，保证客户端读循环只能由心跳失败后的 Close 唤醒。
		<-release
	}))
	defer upstream.Close()
	defer close(release)
	a := newComposeApp(t)
	events, unsub := a.Engine.Bus().Subscribe()
	defer unsub()
	id, err := a.OpenWebSocket(flow.RequestSpec{URL: "ws" + strings.TrimPrefix(upstream.URL, "http")})
	if err != nil {
		t.Fatal(err)
	}
	defer a.CloseWebSocket(id)
	c := a.outWS.get(id)
	if err := c.conn.UnderlyingConn().(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	final := waitWSSession(t, a, events, id, "心跳失败收尾", func(d service.WSSessionDTOType) bool { return d.Status == "closed" })
	if final.EndTime == "" || a.outWS.get(id) != nil {
		t.Fatalf("心跳失败未清理会话: %+v", final)
	}
	awaitAppSignal(t, c.closed, "心跳退出")
}
