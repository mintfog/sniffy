// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

// 本文件覆盖 ws_hub.go 的 headless 广播中心；真实服务器由 listen_test.go 的 startAPIServer 创建。
//
// 绑定真实端口或使用 goroutineDump 的用例串行执行；纯内存 translate 表可并行。

// dialWS 建立 WebSocket 连接并在服务器尚未 accept 时重试。
func dialWS(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/api/ws?token=tok", nil)
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("连接 WebSocket 失败: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readEnvelope 以单次长超时读取一条广播消息；readUntil 用于跳过队列中的无关消息。
func readEnvelope(t *testing.T, conn *websocket.Conn) (int, []byte) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读广播消息失败: %v", err)
	}
	return msgType, data
}

// readUntil 读取直到消息满足 match，并要求途中每条消息都是完整 JSON。
func readUntil(t *testing.T, conn *websocket.Conn, match func(env wsTestEnvelope) bool) (int, []byte) {
	t.Helper()
	for range 32 {
		msgType, data := readEnvelope(t, conn)
		var env wsTestEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatalf("广播消息不是完整 JSON: %v (%s)", err, data)
		}
		if match(env) {
			return msgType, data
		}
	}
	t.Fatal("连读 32 条都没等到目标消息")
	return 0, nil
}

// wsTestEnvelope 是广播信封的解析视图；Payload 使用 any，覆盖对象、数字和 nil。
type wsTestEnvelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// field 返回对象载荷中的字段，其他载荷类型返回 nil。
func (e wsTestEnvelope) field(key string) any {
	m, ok := e.Payload.(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

// waitForRegistered 持续投递探针事件，直到客户端收到一条并确认已注册。
func waitForRegistered(t *testing.T, bus *core.EventBus, conn *websocket.Conn) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			bus.Emit(core.EventFlowStarted, map[string]any{"probe": true})
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()
	readEnvelope(t, conn)
}

// TestTranslateEventTypeTable 固定 headless 与客户端之间的事件类型映射，未列出的类型按原值透传。
func TestTranslateEventTypeTable(t *testing.T) {
	t.Parallel() // 纯内存的表驱动用例:不绑端口、不起 goroutine,不受本文件的并行禁令约束
	cases := []struct {
		in   core.EventType
		want string
	}{
		{core.EventFlowStarted, "http_request"},
		{core.EventFlowCompleted, "http_response"},
		{core.EventFlowUpdated, "session_updated"},
		{core.EventWSMessage, "websocket_session"},
		{core.EventStreamMessage, "stream_session"},
		{core.EventBreakpointHit, "breakpoint_hit"},
		{core.EventBreakpointResolved, "breakpoint_resolved"},

		// 未列名事件按原值透传。
		{core.EventStatsTick, "stats_tick"},
		{core.EventConnStarted, "conn_started"},
		{core.EventConnEnded, "conn_ended"},
		{core.EventPluginReloaded, "plugin_reloaded"},
		{core.EventPluginErrored, "plugin_errored"},
		{core.EventType("brand_new"), "brand_new"},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			t.Parallel()
			payload := map[string]any{"marker": string(c.in)}
			got := translate(core.Event{Type: c.in, Payload: payload})
			if got.Type != c.want {
				t.Errorf("translate(%q).Type = %q,期望 %q", c.in, got.Type, c.want)
			}
			// 载荷保持原引用，修改原 map 后广播视图同步变化。
			payload["marker"] = "mutated"
			gotPayload, ok := got.Payload.(map[string]any)
			if !ok || gotPayload["marker"] != "mutated" {
				t.Errorf("payload 未原样透传: %#v", got.Payload)
			}
		})
	}
}

// TestBroadcastEnvelopeShape 固定信封字段、文本消息类型和 nil/零值载荷的 JSON 形状。
func TestBroadcastEnvelopeShape(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	bus := s.svc.Bus()
	conn := dialWS(t, addr)
	waitForRegistered(t, bus, conn)

	t.Run("带载荷的事件", func(t *testing.T) {
		bus.Emit(core.EventFlowStarted, map[string]any{"id": "f1"})
		msgType, data := readUntil(t, conn, func(e wsTestEnvelope) bool { return e.field("id") == "f1" })
		if msgType != websocket.TextMessage {
			t.Errorf("消息类型 = %d,期望 TextMessage(%d)", msgType, websocket.TextMessage)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("消息不是 JSON 对象: %v (%s)", err, data)
		}
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"payload", "type"}) {
			t.Errorf("信封键 = %v,期望恰为 {payload,type}", keys)
		}
		var env struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatalf("解析信封失败: %v", err)
		}
		if env.Type != "http_request" || env.Payload["id"] != "f1" {
			t.Errorf("信封 = %+v", env)
		}
	})

	// nil payload 按 omitempty 省略 payload 键。
	t.Run("无载荷的事件没有 payload 键", func(t *testing.T) {
		bus.Emit(core.EventConnEnded, nil)
		_, data := readUntil(t, conn, func(e wsTestEnvelope) bool { return e.Type == "conn_ended" })
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("消息不是 JSON 对象: %v", err)
		}
		if _, ok := raw["payload"]; ok {
			t.Errorf("nil 载荷不应出现 payload 键: %s", data)
		}
		if string(raw["type"]) != `"conn_ended"` {
			t.Errorf("type = %s", raw["type"])
		}
	})

	// interface 字段仅在 nil 时省略，0、空串和 false 等零值仍保留。
	t.Run("零值载荷仍保留 payload 键", func(t *testing.T) {
		bus.Emit(core.EventStatsTick, 0)
		_, data := readUntil(t, conn, func(e wsTestEnvelope) bool { return e.Type == "stats_tick" })
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("消息不是 JSON 对象: %v", err)
		}
		if string(raw["payload"]) != "0" {
			t.Errorf("payload = %s,期望 0", raw["payload"])
		}
	})
}

// TestBroadcastReachesAllClients 广播将同一事件送达所有已注册客户端，且各客户端收到相同字节。
func TestBroadcastReachesAllClients(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	bus := s.svc.Bus()
	c1 := dialWS(t, addr)
	waitForRegistered(t, bus, c1)
	c2 := dialWS(t, addr)
	waitForRegistered(t, bus, c2)

	// 两个客户端分别读取带标记的目标事件，跳过注册期间产生的探针。
	isTarget := func(e wsTestEnvelope) bool { return e.field("probe") == "x" }
	bus.Emit(core.EventFlowStarted, map[string]any{"probe": "x"})
	_, d1 := readUntil(t, c1, isTarget)
	_, d2 := readUntil(t, c2, isTarget)
	if !bytes.Equal(d1, d2) {
		t.Errorf("两个客户端收到的字节不同:\n%s\n%s", d1, d2)
	}
	var env struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(d1, &env); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if env.Type != "http_request" || env.Payload["probe"] != "x" {
		t.Errorf("信封 = %+v", env)
	}
}

// TestSlowClientIsDroppedNotBlocking 慢客户端被摘除，健康客户端继续收到广播，验证 fan-out 不受单个订阅者阻塞。
func TestSlowClientIsDroppedNotBlocking(t *testing.T) {
	svc := service.New(nil, core.NewEventBus(), "", "")
	h := newHub(svc)
	h.start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.stop(ctx)
	})

	slow := &wsClient{send: make(chan []byte)}       // 无缓冲且无读者
	healthy := &wsClient{send: make(chan []byte, 4)} // 正常客户端
	h.register <- slow
	h.register <- healthy

	svc.Bus().Emit(core.EventFlowStarted, map[string]any{"n": 1})

	select {
	case data := <-healthy.send:
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &env); err != nil || env.Type != "http_request" {
			t.Errorf("健康客户端收到 %s (err %v)", data, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("健康客户端没收到消息:广播循环可能卡在慢客户端的发送上")
	}

	select {
	case _, ok := <-slow.send:
		if ok {
			t.Error("慢客户端的 channel 应已被关闭(drop),而不是收到消息")
		}
	case <-time.After(2 * time.Second):
		t.Error("慢客户端未被摘除:它的 send channel 仍然开着")
	}

	// 摘除慢客户端后，后续事件继续送达健康客户端。
	svc.Bus().Emit(core.EventFlowStarted, map[string]any{"n": 2})
	select {
	case <-healthy.send:
	case <-time.After(2 * time.Second):
		t.Error("摘除慢客户端后广播循环停止工作")
	}
}

// TestClientDisconnectUnregisters 客户端断开后从 Hub 注销，其他连接和 Hub goroutine 正常收口。
func TestClientDisconnectUnregisters(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	bus := s.svc.Bus()
	a := dialWS(t, addr)
	waitForRegistered(t, bus, a)
	b := dialWS(t, addr)
	waitForRegistered(t, bus, b)

	if err := a.Close(); err != nil {
		t.Fatalf("关闭第一个连接: %v", err)
	}

	// 断开一个客户端后，广播仍送达另一个客户端。
	waitForRegistered(t, bus, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	for _, fn := range []string{"api.(*Hub).run", "api.(*Hub).readPump", "api.(*Hub).writePump"} {
		assertGoroutineGone(t, fn)
	}
}

// TestUnserializableEventIsSkipped 无法 JSON 序列化的事件被跳过，后续可序列化事件继续广播。
func TestUnserializableEventIsSkipped(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	bus := s.svc.Bus()
	conn := dialWS(t, addr)
	waitForRegistered(t, bus, conn)

	bus.Emit(core.EventFlowStarted, map[string]any{"bad": func() {}})
	bus.Emit(core.EventFlowStarted, map[string]any{"probe": "ok"})

	// readUntil 同时验证脏事件未写出半截 JSON 且广播循环继续运行。
	readUntil(t, conn, func(e wsTestEnvelope) bool { return e.field("probe") == "ok" })
}

// TestUpgradeAfterHubStopIsRejected Hub 停止后，新升级连接被关闭，相关 goroutine 不会挂起。
func TestUpgradeAfterHubStopIsRejected(t *testing.T) {
	s, addr := startAPIServer(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.hub.stop(ctx) // 只停 Hub,HTTP 服务器仍在 accept

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/api/ws?token=tok", nil)
	if err != nil {
		t.Fatalf("握手本身应当成功(拒绝发生在升级之后): %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("Hub 已停,新连接不该还能读到消息")
	} else if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
		t.Fatalf("连接既没被关闭也没收到消息,而是读超时:说明它被挂住了(%v)", err)
	}

	assertGoroutineGone(t, "api.(*Hub).readPump")
	assertGoroutineGone(t, "api.(*Hub).writePump")
}

// TestUpgradeFailuresRejected WebSocket 升级执行同源校验、升级格式校验和 GET 方法白名单，失败请求不注册客户端。
func TestUpgradeFailuresRejected(t *testing.T) {
	svc := service.New(nil, core.NewEventBus(), "", "")
	h := newHub(svc)

	t.Run("跨站 Origin 的升级被拒", func(t *testing.T) {
		rec := do(t, http.HandlerFunc(h.handleWS), http.MethodGet, "/api/ws", "",
			withHeader("Connection", "Upgrade"),
			withHeader("Upgrade", "websocket"),
			withHeader("Sec-WebSocket-Version", "13"),
			withHeader("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ=="),
			withHeader("Origin", "http://evil.com"),
		)
		if rec.Code != http.StatusForbidden {
			t.Errorf("状态码 = %d,期望 403", rec.Code)
		}
	})

	t.Run("非升级的普通 GET 被拒", func(t *testing.T) {
		rec := do(t, http.HandlerFunc(h.handleWS), http.MethodGet, "/api/ws", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
	})

	// 非 GET 升级返回带 Allow: GET 的 405。
	t.Run("非 GET 回带 Allow 的 405", func(t *testing.T) {
		rec := do(t, http.HandlerFunc(h.handleWS), http.MethodPost, "/api/ws", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("状态码 = %d,期望 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET" {
			t.Errorf("Allow = %q,期望 \"GET\"", got)
		}
	})

}

// TestStopClosesHubAndUpgradedConns Stop 关闭 Hub、广播循环和已升级的 WebSocket 连接，并发送规范 Close 帧。
func TestStopClosesHubAndUpgradedConns(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	conn := dialWS(t, addr)
	// 先确认链路已注册。
	waitForRegistered(t, s.svc.Bus(), conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("Stop 后已升级的 WebSocket 连接仍可读,应已断开")
	}
	// 只接受服务端发送的规范 Close 帧，确保客户端能识别优雅关停。
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
		t.Fatalf("期望收到规范的 Close 帧,实际错误: %v", err)
	}

	for _, fn := range []string{"api.(*Hub).run", "api.(*Hub).readPump", "api.(*Hub).writePump"} {
		assertGoroutineGone(t, fn)
	}
}

// TestStopIsIdempotent 重复 Stop 保持幂等。
func TestStopIsIdempotent(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	conn := dialWS(t, addr)
	waitForRegistered(t, s.svc.Bus(), conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 3 {
		if err := s.Stop(ctx); err != nil {
			t.Fatalf("重复 Stop 失败: %v", err)
		}
	}
}

// TestStopWithoutServeDoesNotBlock Listen 后未 Serve 的服务器可立即 Stop。
func TestStopWithoutServeDoesNotBlock(t *testing.T) {
	s := New(service.New(nil, core.NewEventBus(), "", ""), nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	// 本用例未启动 Serve，清理函数直接关闭 listener。
	t.Cleanup(func() { _ = s.listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Stop(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop 失败: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未 Serve 时 Stop 阻塞了")
	}
}

// TestHubStopHonoursContextDeadline Hub 未结束时 stop 遵守 ctx 截止时间。
func TestHubStopHonoursContextDeadline(t *testing.T) {
	h := newHub(service.New(nil, core.NewEventBus(), "", ""))
	// 标记 Hub 已启动但不运行 run，构造等待 ctx 的场景。
	h.started.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// 使用 ctx 自己的 deadline 作为时序基准。
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("ctx 没有 deadline")
	}

	done := make(chan struct{})
	go func() {
		h.stop(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop 无视 ctx 预算挂死了")
	}
	if time.Now().Before(deadline) {
		t.Errorf("stop 在 ctx 到期前 %v 就返回了 —— 它可能根本没在等 run 退出", time.Until(deadline))
	}
}

// goroutineDump 返回全进程栈；使用它的用例需串行执行并在清理函数中关闭服务器。
func goroutineDump() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

func assertGoroutineGone(t *testing.T, fn string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(goroutineDump(), fn) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("Stop 后 goroutine 仍存活: %s(全进程扫描,也可能是同包其它用例泄漏的)", fn)
}
