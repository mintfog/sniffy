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

// 本文件对应 ws_hub.go:headless 模式的广播中心。用例绑真实端口、并用全进程 goroutine 快照做断言,
// 一律禁止 t.Parallel(见 goroutineDump 的注释)。服务器由 listen_test.go 的 startAPIServer 起。

// dialWS 拨一条 WebSocket,连不上就重试到超时。
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

// readEnvelope 读一条广播消息并解成信封。
//
// 每次都用一次长超时读:gorilla 的读错误是粘性的(conn.readErr 一旦置位,后续 ReadMessage 立即
// 返回同一错误),「短超时 + 重试」在同一条连接上会退化成忙转,约 1000 次后命中 gorilla 的
// panic("repeated read on failed websocket connection") 把整个测试二进制打挂。
// 同理:任何「按超时判定队列已空」的清空做法都会废掉连接,要跳过无关消息只能用 readUntil。
func readEnvelope(t *testing.T, conn *websocket.Conn) (int, []byte) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读广播消息失败: %v", err)
	}
	return msgType, data
}

// readUntil 一直读到满足 match 的那条消息,跳过在此之前排队的探针事件。
// 途中任何一条消息解不出 JSON 都直接失败 —— 那正是「广播写出了半截 JSON」的样子。
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

// wsTestEnvelope 是广播信封的解析视图(与产品侧 wsEnvelope 分开,好让用例断言线上字段名)。
// Payload 解成 any 而不是 map:载荷本就可以是任意值(数字、nil),限死成对象会让
// 无关的用例在解析阶段就失败。
type wsTestEnvelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// field 取载荷对象里的一个字段;载荷不是对象时返回 nil。
func (e wsTestEnvelope) field(key string) any {
	m, ok := e.Payload.(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

// waitForRegistered 反复投递事件直到客户端收到一条,以此确认它已入册
// (register 是异步的,升级成功不代表已进 clients)。后台 ticker 持续投递,前台只做一次长超时读。
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

// TestTranslateEventTypeTable 这张表是 headless 模式与客户端唯一的消息类型约定:改错一个字符串,
// 后端照常广播、HTTP 全绿、日志无错,订阅方静默收不到消息 —— 用户看到「抓到包了但列表不刷新 /
// 断点弹窗不出现」,没有任何一处会报错。default 决定了新增事件类型无需改 hub 即可透传。
func TestTranslateEventTypeTable(t *testing.T) {
	t.Parallel()
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

		// default 分支:未列名的事件类型原样透传,新增事件不必改 hub。
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
			// 载荷必须原样透传而不是被复制:改一下原 map,拿到的那份也要跟着变。
			payload["marker"] = "mutated"
			gotPayload, ok := got.Payload.(map[string]any)
			if !ok || gotPayload["marker"] != "mutated" {
				t.Errorf("payload 未原样透传: %#v", got.Payload)
			}
		})
	}
}

// TestBroadcastEnvelopeShape 信封字段名是与所有 headless 客户端的硬契约:Type 的 tag 敲成 "event"
// 后端毫无异常,而客户端按 type 分发的 switch 静默走空;TextMessage 变 BinaryMessage 则让浏览器侧
// event.data 从 string 变 Blob,JSON.parse 直接抛错。
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

	// payload 为 nil 时整个键消失,客户端必须写 e.payload?.x 而不是 e.payload.x。
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

	// omitempty 对 interface 字段只在 nil 时省略:0 / "" / false 这些零值仍要保留。
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

// TestBroadcastReachesAllClients 现有测试全是单客户端,广播 for 循环实际只跑过 1 次迭代。
// 改成只发第一个客户端、或把 data 换成每客户端复用的可变 buffer,单客户端测试依旧全绿,
// 而用户看到的是「第二个窗口只有第一个窗口不动时才更新」这种极难归因的现象。
func TestBroadcastReachesAllClients(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	bus := s.svc.Bus()
	c1 := dialWS(t, addr)
	waitForRegistered(t, bus, c1)
	c2 := dialWS(t, addr)
	waitForRegistered(t, bus, c2)

	// c1 在等 c2 入册期间也收到了探针事件,故两边都读到带标记的那条为止。
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

// TestSlowClientIsDroppedNotBlocking 这是「非阻塞 fan-out,慢订阅者丢消息」在 transport 层的落点。
// 改成阻塞发送或去掉 drop 后,一个卡住不读的前端(标签页被冻结、断点弹窗挡住渲染)就能让整个
// 广播 goroutine 停在那条 send 上:所有其他客户端停止收事件,表现是全体前端集体假死,重启才恢复。
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

	// 广播循环没被卡住,后续事件照常送达。
	svc.Bus().Emit(core.EventFlowStarted, map[string]any{"n": 2})
	select {
	case <-healthy.send:
	case <-time.After(2 * time.Second):
		t.Error("摘除慢客户端后广播循环停止工作")
	}
}

// TestClientDisconnectUnregisters run 的 unregister 分支与 readPump 的 `case h.unregister <- c` 此前
// 双双为 0 —— 正常的客户端断开在测试里从没执行过,而生产里每次前端关标签页都会走。
// drop 的幂等守卫是唯一防线,去掉它就是 close of closed channel,panic 在 run 的 goroutine 里
// 会打死整个进程:用户看到的是刷新页面时抓包服务整个挂掉。
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

	// 断开一个之后广播仍要送到另一个,且进程不 panic。
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

// TestUnserializableEventIsSkipped Payload 是 any,从 service/pipeline 一路透传,谁往里塞了带 func/chan
// 字段的结构体这里就会 marshal 失败。现在是 continue;若有人改成 return 或忘了 continue,
// 一条脏事件就能让广播循环退出或写出半截 JSON —— 用户侧表现是抓包途中前端突然永久停更,
// 而 err 被丢弃、服务端一行日志都没有。
func TestUnserializableEventIsSkipped(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	bus := s.svc.Bus()
	conn := dialWS(t, addr)
	waitForRegistered(t, bus, conn)

	bus.Emit(core.EventFlowStarted, map[string]any{"bad": func() {}})
	bus.Emit(core.EventFlowStarted, map[string]any{"probe": "ok"})

	// readUntil 会在途中任何一条消息解不出 JSON 时失败,脏事件写出半截 JSON 就在这里露馅;
	// 而广播循环若被脏事件带退出,则永远等不到 probe 那条。
	readUntil(t, conn, func(e wsTestEnvelope) bool { return e.field("probe") == "ok" })
}

// TestUpgradeAfterHubStopIsRejected Stop 的注释「先停 Hub,让此刻正在升级的连接直接被拒」此前是纯口头
// 承诺。select 一旦退化成无条件 h.register <- c,关机窗口里接进来的客户端会永远阻塞在发送 register
// (run 已退出、没人接收),该请求的 goroutine 与连接一起挂住、Shutdown 也等不到它:退出流程卡死。
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

// TestUpgradeFailuresRegisterNothing 「保留 gorilla 默认的同源校验」是一句写在注释里的安全声明。
// 哪天有人为了让某个调试工具连上而写下 CheckOrigin 恒 true,这行改动不会让任何测试变红 ——
// 而它把跨站防线在 WS 端点上整个拆掉:恶意页面可从浏览器直连回环端口拿到全量抓包流
// (含所有请求头里的 Cookie 与 Authorization),而 /api/ws 恰恰又放宽到接受 ?token=。
func TestUpgradeFailuresRegisterNothing(t *testing.T) {
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

	// Upgrade 自己也会拒绝非 GET,但它回的 405 不带 Allow,与其余端点的契约对不上。
	t.Run("非 GET 回带 Allow 的 405", func(t *testing.T) {
		rec := do(t, http.HandlerFunc(h.handleWS), http.MethodPost, "/api/ws", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("状态码 = %d,期望 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET" {
			t.Errorf("Allow = %q,期望 \"GET\"", got)
		}
	})

	if n := len(h.clients); n != 0 {
		t.Errorf("升级失败不应注册客户端,实际 %d 个", n)
	}
}

// TestStopClosesHubAndUpgradedConns http.Server.Shutdown 不会关闭被 hijack 的 WebSocket 连接,
// 也不会让广播循环退出 —— 这些必须由 Stop 自己收口。
func TestStopClosesHubAndUpgradedConns(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	conn := dialWS(t, addr)
	// 先确认链路是通的,否则后面的断言可能只是「本来就没收到」。
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
	// 不接受 CloseAbnormalClosure(1006):那表示服务端根本没发 Close 帧。把它算作通过,
	// 等于让「drop 会让 writePump 发出 Close 帧」这条断言形同虚设,而前端会把优雅关停
	// 误判成网络故障并进入退避重连。
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
		t.Fatalf("期望收到规范的 Close 帧,实际错误: %v", err)
	}

	for _, fn := range []string{"api.(*Hub).run", "api.(*Hub).readPump", "api.(*Hub).writePump"} {
		assertGoroutineGone(t, fn)
	}
}

// TestStopIsIdempotent 重复 Stop 不能 panic(close of closed channel)。
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

// TestStopWithoutServeDoesNotBlock Listen 后未 Serve 就 Stop:广播循环从未启动,
// stop 不该干等到 ctx 超时。
func TestStopWithoutServeDoesNotBlock(t *testing.T) {
	s := New(service.New(nil, core.NewEventBus(), "", ""), nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
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

// TestHubStopHonoursContextDeadline stop 的 ctx.Done 分支此前计数为 0 —— 现有用例要么 run 正常退出、
// 要么 started=false 提前 return。把 select 化简成裸 <-h.stopped(看上去更直白且现有测试全绿)后,
// 只要 run 因任何原因不退出,关停就会无视 ctx 永久挂死:Ctrl+C 无反应,只能 kill。
func TestHubStopHonoursContextDeadline(t *testing.T) {
	h := newHub(service.New(nil, core.NewEventBus(), "", ""))
	// 只声明「已启动」而不真的跑 run:stopped 永远不会关闭,唯一的出口就是 ctx。
	h.started.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
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
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("stop 在 %v 就返回了,没等到 ctx 到期 —— 它可能根本没在等 run 退出", elapsed)
	}
}

// goroutineDump 取全进程栈。注意它是**全进程**快照:断言的其实是「整个包里没有任何该帧」,
// 所以 ws_hub_test.go 与 listen_test.go 的用例禁止 t.Parallel,且每台服务器都必须经
// startAPIServer 的 t.Cleanup 收口 —— 一次泄漏会把后续用例一起冤枉。
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
