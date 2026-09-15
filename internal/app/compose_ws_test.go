// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

type wsFrame struct {
	mt   int
	data []byte
}

// wsTestServer 是一个可回显、可主动下推、可主动断开的 WebSocket 上游。
// 服务端一侧同样受 gorilla「只允许一个并发写者」的约束,故所有写都过 writeMu。
type wsTestServer struct {
	url      string
	headers  chan http.Header
	received chan wsFrame
	control  chan wsFrame

	mu      sync.Mutex
	conn    *gws.Conn
	writeMu sync.Mutex
	echo    bool
}

func newWSTestServer(t *testing.T, echo bool) *wsTestServer {
	t.Helper()
	s := &wsTestServer{
		headers:  make(chan http.Header, 8),
		received: make(chan wsFrame, 2048),
		control:  make(chan wsFrame, 32),
		echo:     echo,
	}
	up := gws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.headers <- r.Header.Clone():
		default:
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		s.mu.Lock()
		s.conn = c
		s.mu.Unlock()

		c.SetPingHandler(func(payload string) error {
			select {
			case s.control <- wsFrame{gws.PingMessage, []byte(payload)}:
			default:
			}
			return nil
		})
		c.SetCloseHandler(func(code int, text string) error {
			select {
			case s.control <- wsFrame{gws.CloseMessage, []byte(text)}:
			default:
			}
			// 覆写 close handler 会丢掉 gorilla 的默认回帧,这里补回来。
			s.writeMu.Lock()
			defer s.writeMu.Unlock()
			_ = c.WriteControl(gws.CloseMessage, gws.FormatCloseMessage(code, ""), time.Now().Add(time.Second))
			return nil
		})

		for {
			mt, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			select {
			case s.received <- wsFrame{mt, append([]byte(nil), data...)}:
			default:
			}
			if s.echo {
				s.writeMu.Lock()
				err = c.WriteMessage(mt, data)
				s.writeMu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	s.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	return s
}

// push 由服务端主动下推一帧。
func (s *wsTestServer) push(t *testing.T, mt int, data []byte) {
	t.Helper()
	c := s.waitConn(t)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := c.WriteMessage(mt, data); err != nil {
		t.Fatalf("服务端下推失败: %v", err)
	}
}

// closeConn 由服务端主动断开(不发关闭帧,模拟对端直接掉线)。
func (s *wsTestServer) closeConn(t *testing.T) {
	t.Helper()
	_ = s.waitConn(t).Close()
}

func (s *wsTestServer) waitConn(t *testing.T) *gws.Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		c := s.conn
		s.mu.Unlock()
		if c != nil {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("服务端连接始终未建立")
	return nil
}

func (s *wsTestServer) nextFrameHeader(t *testing.T) http.Header {
	t.Helper()
	select {
	case h := <-s.headers:
		return h
	case <-time.After(5 * time.Second):
		t.Fatal("等待握手请求头超时")
		return nil
	}
}

func (s *wsTestServer) nextFrame(t *testing.T, ch chan wsFrame, what string) wsFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(5 * time.Second):
		t.Fatalf("等待超时: %s", what)
		return wsFrame{}
	}
}

// waitWSSession 等一条满足 want 的 WebSocket 会话快照(经事件总线,不 sleep 轮询)。
// 与 waitStreamSession 同理:推送只带增量,整条会话从 store 取。
func waitWSSession(t *testing.T, app *App, ch <-chan core.Event, id, what string, want func(service.WSSessionDTOType) bool) service.WSSessionDTOType {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != core.EventWSMessage {
				continue
			}
			d, ok := ev.Payload.(service.WSDeltaDTO)
			if !ok || d.Session.ID != id {
				continue
			}
			if dto, found := app.Service.WSSession(id); found && want(dto) {
				return dto
			}
		case <-deadline:
			t.Fatalf("等待 WebSocket 会话超时: %s", what)
			return service.WSSessionDTOType{}
		}
	}
}

func TestOpenWebSocketRecordsSession(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })
	if id == "" {
		t.Fatal("OpenWebSocket 未返回会话 id")
	}

	ws, ok := app.Service.WSSession(id)
	if !ok {
		t.Fatal("会话未入库")
	}
	if ws.Status != "open" {
		t.Fatalf("状态 = %q,期望 open", ws.Status)
	}
	if ws.URL != srv.url {
		t.Fatalf("URL = %q,期望 %q", ws.URL, srv.url)
	}
	// 出站 WebSocket 不产生 HTTP Flow(与捕获侧一致)。
	if _, ok := app.Service.RawFlow(id); ok {
		t.Fatal("出站 WebSocket 不该产生 HTTP flow")
	}
}

func TestOpenWebSocketNormalizesScheme(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)
	httpURL := "http" + strings.TrimPrefix(srv.url, "ws")

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: httpURL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })
	ws, _ := app.Service.WSSession(id)
	if ws.URL != srv.url {
		t.Fatalf("http:// 应归一为 ws://,实际 %q", ws.URL)
	}

	// 归一化本身不需要真连得上,拿错误文案里的地址反推即可。
	if _, err := app.OpenWebSocket(flow.RequestSpec{URL: "https://127.0.0.1:1/x"}); err == nil {
		t.Fatal("期望连接失败")
	}
	if _, err := app.OpenWebSocket(flow.RequestSpec{URL: "127.0.0.1:1/x"}); err == nil {
		t.Fatal("期望连接失败")
	}

	_, err = app.OpenWebSocket(flow.RequestSpec{URL: "ws://user:pass@127.0.0.1:1/x"})
	if err == nil || !strings.Contains(err.Error(), "用户名密码") {
		t.Fatalf("带 userinfo 的 URL 应给出可读错误,实际 %v", err)
	}
}

// 裸 host 补 wss:猜错时握手立刻失败可见,反过来会把本该加密的流量明文发出去。
func TestOpenWebSocketDefaultsToWSS(t *testing.T) {
	got, err := composeWSURL("ikun.com/socket")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "wss://ikun.com/socket" {
		t.Fatalf("归一化结果 = %q", got)
	}
}

func TestOpenWebSocketDropsHandshakeHeaders(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url, Headers: [][2]string{
		{"Connection", "keep-alive"},
		{"Upgrade", "h2c"},
		{"Sec-WebSocket-Key", "aaaaaaaaaaaaaaaaaaaaaa=="},
		{"Sec-WebSocket-Version", "8"},
		{"Sec-WebSocket-Extensions", "permessage-deflate"},
		{"Authorization", "Bearer t0ken"},
		{"Origin", "https://app.test"},
		{"Sec-WebSocket-Protocol", "graphql-ws"},
	}})
	if err != nil {
		t.Fatalf("用户写的握手头应被剔除而不是让拨号失败: %v", err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })

	h := srv.nextFrameHeader(t)
	for _, name := range []string{"Connection", "Upgrade", "Sec-Websocket-Key", "Sec-Websocket-Version"} {
		if got := h.Values(name); len(got) != 1 {
			t.Fatalf("%s 应恰好出现一次,实际 %v", name, got)
		}
	}
	if h.Get("Upgrade") != "websocket" {
		t.Fatalf("Upgrade = %q,应由 Dialer 独占", h.Get("Upgrade"))
	}
	if h.Get("Sec-Websocket-Version") != "13" {
		t.Fatalf("Sec-WebSocket-Version = %q,应由 Dialer 独占", h.Get("Sec-Websocket-Version"))
	}
	if h.Get("Authorization") != "Bearer t0ken" {
		t.Fatalf("Authorization = %q", h.Get("Authorization"))
	}
	if h.Get("Origin") != "https://app.test" {
		t.Fatalf("Origin = %q", h.Get("Origin"))
	}
	if h.Get("Sec-Websocket-Protocol") != "graphql-ws" {
		t.Fatalf("Sec-WebSocket-Protocol = %q", h.Get("Sec-Websocket-Protocol"))
	}
}

// TestOpenWebSocketHandshakeWireShape 钉住握手报文的写线形态:gorilla Dialer 把头交给
// net/http 的 Request.Write,于是 Host 与 User-Agent 固定在最前,其余头按名字的字节序排列
// (同名头保持键入顺序、头名规范化),Content-Length / Transfer-Encoding / Trailer 不写出。
// 构造器前端按同一形态预览握手报文(web/src/workbench/views/compose/wire.ts 的
// handshakeWire 与 wire.test.ts 的同名断言),两侧必须一致,否则预览与实发不符。
func TestOpenWebSocketHandshakeWireShape(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	head := make(chan []string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			head <- nil
			return
		}
		defer conn.Close()
		var lines []string
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if line = strings.TrimSuffix(line, "\r\n"); line != "" {
				lines = append(lines, line)
			}
			if err != nil || line == "" {
				break
			}
		}
		head <- lines
	}()

	app := newComposeApp(t)
	// 监听方读完请求头就断开,握手必然失败;这里只断言已经写到线上的字节。
	id, err := app.OpenWebSocket(flow.RequestSpec{URL: "ws://" + ln.Addr().String() + "/chat?q=1", Headers: [][2]string{
		{"host", "vhost.test"},
		{"Host", "vhost2.test"},
		{"x-foo", "1"},
		{"X-Foo", "2"},
		{"authorization", "Bearer t"},
		{"Keep-Alive", "timeout=5"},
		{"sec-websocket-protocol", "graphql-ws"},
		{"Sec-WebSocket-Accept", "zz"},
		{"Cookie", "a=b"},
		{"Origin", "https://app.test"},
		{"Content-Length", "5"},
		{"Transfer-Encoding", "chunked"},
		{"Trailer", "X-T"},
	}})
	if err == nil {
		_ = app.CloseWebSocket(id)
	}

	got := <-head
	// Sec-WebSocket-Key 每次握手随机,只校验长度后归一化。
	for i, line := range got {
		if v, ok := strings.CutPrefix(line, "Sec-WebSocket-Key: "); ok {
			if len(v) != 24 {
				t.Fatalf("Sec-WebSocket-Key = %q,应是 16 字节的 base64", v)
			}
			got[i] = "Sec-WebSocket-Key: <随机>"
		}
	}
	want := []string{
		"GET /chat?q=1 HTTP/1.1",
		"Host: vhost2.test",
		"User-Agent: Go-http-client/1.1",
		"Authorization: Bearer t",
		"Connection: Upgrade",
		"Cookie: a=b",
		"Keep-Alive: timeout=5",
		"Origin: https://app.test",
		"Sec-WebSocket-Key: <随机>",
		"Sec-WebSocket-Protocol: graphql-ws",
		"Sec-WebSocket-Version: 13",
		"Sec-Websocket-Accept: zz",
		"Upgrade: websocket",
		"X-Foo: 1",
		"X-Foo: 2",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("握手报文与预览形态不符:\n实际 %q\n期望 %q", got, want)
	}
}

func TestSendWSMessageRecordsOutboundAndInbound(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })

	if err := app.SendWSMessage(id, flow.WSText, "hello"); err != nil {
		t.Fatal(err)
	}
	if got := srv.nextFrame(t, srv.received, "服务端收帧"); string(got.data) != "hello" {
		t.Fatalf("服务端收到 %q,期望 %q", string(got.data), "hello")
	}

	ws := waitWSSession(t, app, events, id, "一发一回", func(d service.WSSessionDTOType) bool {
		return d.MessageCount >= 2
	})
	if ws.MessageCount != 2 {
		t.Fatalf("消息数 = %d,期望 2", ws.MessageCount)
	}
	if ws.Messages[0].Direction != "outbound" || ws.Messages[1].Direction != "inbound" {
		t.Fatalf("方向 = %q %q", ws.Messages[0].Direction, ws.Messages[1].Direction)
	}
	if ws.Messages[0].Binary || ws.Messages[1].Binary {
		t.Fatal("文本帧不该被标成 binary")
	}
	if ws.TotalSize != int64(2*len("hello")) {
		t.Fatalf("总字节 = %d,期望 %d", ws.TotalSize, 2*len("hello"))
	}
}

func TestSendWSMessageBinaryRoundTrip(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })

	raw := []byte{0x00, 0xff, 0xfe, 0x41}
	if err := app.SendWSMessage(id, flow.WSBinary, base64.StdEncoding.EncodeToString(raw)); err != nil {
		t.Fatal(err)
	}
	got := srv.nextFrame(t, srv.received, "服务端收二进制帧")
	if got.mt != gws.BinaryMessage {
		t.Fatalf("opcode = %d,期望 binary", got.mt)
	}
	if string(got.data) != string(raw) {
		t.Fatalf("服务端收到 %v,期望 %v", got.data, raw)
	}

	ws := waitWSSession(t, app, events, id, "二进制回程", func(d service.WSSessionDTOType) bool {
		return d.MessageCount >= 2
	})
	if !ws.Messages[0].Binary || ws.Messages[0].Data != base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("出站帧 DTO = %+v", ws.Messages[0])
	}
}

func TestSendWSMessagePingNotRecorded(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })

	if err := app.SendWSMessage(id, flow.WSPing, base64.StdEncoding.EncodeToString([]byte("pp"))); err != nil {
		t.Fatal(err)
	}
	if got := srv.nextFrame(t, srv.control, "服务端收 ping"); string(got.data) != "pp" {
		t.Fatalf("ping 载荷 = %q", string(got.data))
	}
	ws, _ := app.Service.WSSession(id)
	if ws.MessageCount != 0 {
		t.Fatalf("ping 不该记入消息时间线,实际 %d 条", ws.MessageCount)
	}
}

func TestSendWSMessageRejectsCloseType(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })

	if err := app.SendWSMessage(id, flow.WSClose, ""); err == nil {
		t.Fatal("close 类型应被拒绝")
	} else if !strings.Contains(err.Error(), "CloseWebSocket") {
		t.Fatalf("错误文案应指向 CloseWebSocket,实际 %q", err.Error())
	}
	if err := app.SendWSMessage(id, flow.WSText, "still alive"); err != nil {
		t.Fatalf("连接应仍然可用: %v", err)
	}
}

func TestCloseWebSocketClosesSessionAndConnection(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.CloseWebSocket(id); err != nil {
		t.Fatal(err)
	}
	srv.nextFrame(t, srv.control, "服务端收 close 帧")

	final := waitWSSession(t, app, events, id, "会话收尾", func(d service.WSSessionDTOType) bool {
		return d.Status == "closed"
	})
	if final.EndTime == "" {
		t.Fatal("收尾的会话应带 EndTime")
	}
	if err := app.CloseWebSocket(id); err != nil {
		t.Fatalf("重复关闭应返回 nil,实际 %v", err)
	}
	if err := app.SendWSMessage(id, flow.WSText, "x"); err == nil {
		t.Fatal("关闭后发送应报错")
	}
}

func TestOpenWebSocketRejectsBadInput(t *testing.T) {
	app := newComposeApp(t)
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(forbidden.Close)

	cases := []struct {
		name string
		url  string
		want string
	}{
		{"空 URL", "   ", "为空"},
		{"不支持的协议", "ftp://example.com/x", "不支持的协议"},
		{"无法解析", "ws://exa mple.com/x", "无法解析"},
		{"缺少主机名", "ws:///onlypath", "缺少主机名"},
		{"服务端拒绝升级", "ws" + strings.TrimPrefix(forbidden.URL, "http"), "403"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := app.OpenWebSocket(flow.RequestSpec{URL: tc.url})
			if err == nil {
				t.Fatalf("期望报错,却返回了会话 id %q", id)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误信息 %q 不含 %q", err.Error(), tc.want)
			}
			if id != "" {
				t.Fatalf("失败时不该产生会话,却返回了 %q", id)
			}
		})
	}
	// 握手失败最容易漏的是「已经 push 了一条 open 的空壳会话」。
	if list, total := app.Service.WSSessions(1, 100); total != 0 || len(list) != 0 {
		t.Fatalf("握手失败不该留下任何会话记录,实际 %d 条", total)
	}
}

func TestOpenWebSocketHandshakeFailureCarriesStatus(t *testing.T) {
	app := newComposeApp(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	_, err := app.OpenWebSocket(flow.RequestSpec{URL: "ws" + strings.TrimPrefix(srv.URL, "http")})
	if err == nil {
		t.Fatal("期望握手失败")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("错误文案应含状态码,实际 %q", err.Error())
	}
}

func TestUpstreamCloseMarksSessionClosed(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	srv.closeConn(t)

	waitWSSession(t, app, events, id, "上游断开后自动收尾", func(d service.WSSessionDTOType) bool {
		return d.Status == "closed"
	})
	if app.outWS.get(id) != nil {
		t.Fatal("读循环退出后注册表条目应已摘除")
	}
}

// 条数封顶只裁剪展示用的时间线,计数与总大小必须继续累加。
func TestWSSessionMessageCapDoesNotLoseCount(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, false)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })

	const n = flow.MaxWSMessages + 20
	for i := 0; i < n; i++ {
		if err := app.SendWSMessage(id, flow.WSText, "x"); err != nil {
			t.Fatalf("第 %d 条发送失败: %v", i, err)
		}
	}
	ws, _ := app.Service.WSSession(id)
	if ws.MessageCount != n {
		t.Fatalf("消息计数 = %d,期望 %d", ws.MessageCount, n)
	}
	if ws.TotalSize != int64(n) {
		t.Fatalf("总字节 = %d,期望 %d", ws.TotalSize, n)
	}
	if len(ws.Messages) != flow.MaxWSMessages {
		t.Fatalf("时间线长度 = %d,期望封顶在 %d", len(ws.Messages), flow.MaxWSMessages)
	}
}

func TestWSRegistryRejectsBeyondLimit(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)

	ids := make([]string, 0, maxComposeWS)
	for i := 0; i < maxComposeWS; i++ {
		id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
		if err != nil {
			t.Fatalf("第 %d 条连接失败: %v", i, err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(app.CloseAllWebSockets)

	if _, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url}); err == nil {
		t.Fatal("超出上限应返回错误")
	} else if !strings.Contains(err.Error(), "16") {
		t.Fatalf("错误文案应含上限数字,实际 %q", err.Error())
	}
	if ws, _ := app.Service.WSSession(ids[0]); ws.Status != "open" {
		t.Fatalf("已建连接不该受影响,实际状态 %q", ws.Status)
	}

	// 上限是活动计数而非单调计数:关掉一条就该能再开一条。
	_ = app.CloseWebSocket(ids[0])
	waitRegistrySize(t, app, maxComposeWS-1)
	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatalf("腾出名额后应能再开: %v", err)
	}
	_ = app.CloseWebSocket(id)
}

func waitRegistrySize(t *testing.T, app *App, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		app.outWS.mu.Lock()
		n := len(app.outWS.conns)
		app.outWS.mu.Unlock()
		if n == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("注册表规模未收敛到 %d", want)
}

// stalledWSServer 接受 TCP 连接后不作任何应答:握手一路挂到测试收尾才被打断。
// 用它才能观察到「拨号进行中」这个中间态 —— 正常上游的握手快到根本抓不住。
func stalledWSServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close() // 断开后挂着的握手立刻报错返回,goroutine 不必干等握手超时
		}
	})
	return "ws://" + ln.Addr().String()
}

// 名额必须在拨号之前占住。若等握手成功再检查,握手最长 15 秒的窗口里并发请求可以各自
// 拨号,上限就只约束了「同时活着的连接数」,约束不住「同时占着的 socket 数」。
func TestWSRegistryReservesSlotBeforeDial(t *testing.T) {
	app := newComposeApp(t)
	// 先登记等待:清理按后进先出跑,这样 stalledWSServer 的断连先发生,挂着的握手当场
	// 报错返回,wg.Wait 不必干等 15 秒的握手超时。
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	url := stalledWSServer(t)

	for i := 0; i < maxComposeWS; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id, err := app.OpenWebSocket(flow.RequestSpec{URL: url}); err == nil {
				_ = app.CloseWebSocket(id)
			}
		}()
	}
	waitDialingSize(t, app, maxComposeWS)

	// 全部名额都还卡在握手里,此时再开一条必须当场被拒,而不是先去拨第 17 个 socket。
	if _, err := app.OpenWebSocket(flow.RequestSpec{URL: url}); err == nil {
		t.Fatal("握手中的连接也应占名额,超出上限时必须拒绝")
	} else if !strings.Contains(err.Error(), "16") {
		t.Fatalf("错误文案应含上限数字,实际 %q", err.Error())
	}
}

func waitDialingSize(t *testing.T, app *App, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		app.outWS.mu.Lock()
		n := len(app.outWS.dials)
		app.outWS.mu.Unlock()
		if n == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("握手中的连接数未收敛到 %d", want)
}

// 窗口在握手期间关闭:名额被 closeAll 收走后,后到的握手成功不能再登记进注册表 ——
// 否则就留下一条谁也够不着、只能等进程退出的活连接。
func TestWSRegistryBindAfterCloseAllIsRejected(t *testing.T) {
	var reg composeWSRegistry
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := reg.reserve("ws-1", cancel); err != nil {
		t.Fatal(err)
	}
	reg.closeAll()
	if ctx.Err() == nil {
		t.Fatal("closeAll 未取消握手中的拨号")
	}
	if reg.bind(&composeWSConn{session: &flow.WSSession{ID: "ws-1"}}) {
		t.Fatal("名额已被收走,bind 必须失败(调用方据此就地关闭连接)")
	}
	if reg.get("ws-1") != nil {
		t.Fatal("被拒绝的连接不该留在注册表里")
	}
}

// 一帧从入口到界面要被复制好几遍,上限必须落在 App 层:桌面 Bridge 直接调 SendWSMessage,
// API 层的请求体上限管不到它。
func TestSendWSMessageRejectsOversizedPayload(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, false)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })

	err = app.SendWSMessage(id, flow.WSText, strings.Repeat("x", composeWSWriteLimit+1))
	if err == nil {
		t.Fatal("超过单帧上限的载荷必须被拒绝")
	}
	if !strings.Contains(err.Error(), "上限") {
		t.Fatalf("错误文案未说明是超上限: %q", err.Error())
	}
	// base64 的长度不等于载荷长度,判定必须发生在解码之后。
	oversized := base64.StdEncoding.EncodeToString(make([]byte, composeWSWriteLimit+1))
	if err := app.SendWSMessage(id, flow.WSBinary, oversized); err == nil {
		t.Fatal("解码后超限的 binary 帧必须被拒绝")
	}
	if ws, _ := app.Service.WSSession(id); ws.MessageCount != 0 {
		t.Fatalf("被拒的帧不该进会话,实际记了 %d 条", ws.MessageCount)
	}
}

// ping 走控制帧,协议把载荷卡在 125 字节(RFC 6455 §5.5),比数据帧的上限严得多。
// 不在入口拦下的话,gorilla 只会回一句 "invalid control frame",看不出该改哪里。
func TestSendWSMessagePingRejectsOversizedControlFrame(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, false)

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })

	tooBig := base64.StdEncoding.EncodeToString(make([]byte, maxControlFramePayload+1))
	err = app.SendWSMessage(id, flow.WSPing, tooBig)
	if err == nil {
		t.Fatal("超过控制帧上限的 ping 必须被拒绝")
	}
	if !strings.Contains(err.Error(), "控制帧") {
		t.Fatalf("错误文案未点明控制帧上限: %q", err.Error())
	}
	// 恰好等于上限的仍要放行,连接也不能被前一次的拒绝弄坏。
	ok := base64.StdEncoding.EncodeToString(make([]byte, maxControlFramePayload))
	if err := app.SendWSMessage(id, flow.WSPing, ok); err != nil {
		t.Fatalf("125 字节的 ping 应当放行: %v", err)
	}
	if err := app.SendWSMessage(id, flow.WSText, "still alive"); err != nil {
		t.Fatalf("被拒的 ping 不该影响后续发送: %v", err)
	}
}

// 50 个 goroutine 交错开/发/关:注册表与会话记录都必须扛住,且最终不留残余。
func TestWSRegistryConcurrentOpenClose(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
			if err != nil {
				return // 撞上并发上限是预期结果,不是失败
			}
			_ = app.SendWSMessage(id, flow.WSText, "ping")
			_ = app.CloseWebSocket(id)
		}()
	}
	wg.Wait()
	app.CloseAllWebSockets()
	waitRegistrySize(t, app, 0)
}

func TestCloseAllWebSocketsClosesEverything(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)
	before := runtime.NumGoroutine()

	ids := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	app.CloseAllWebSockets()
	waitRegistrySize(t, app, 0)

	for _, id := range ids {
		ws, ok := app.Service.WSSession(id)
		if !ok || ws.Status != "closed" {
			t.Fatalf("会话 %s ok=%v status=%q", id, ok, ws.Status)
		}
	}
	// goroutine 数只能粗判:测试进程里还有 httptest 的连接在收尾,留足松弛量。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+8 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine 数未回落: before=%d after=%d", before, runtime.NumGoroutine())
}

// connectProxy 是一个只支持 CONNECT 的最小代理,用于验证出站 WebSocket 确实经过上游代理。
func connectProxy(t *testing.T) (addr string, hits *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	hits = &atomic.Int64{}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				br := bufio.NewReader(conn)
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				for {
					h, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimRight(h, "\r\n") == "" {
						break
					}
				}
				parts := strings.Fields(line)
				if len(parts) < 2 || parts[0] != "CONNECT" {
					_, _ = conn.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
					return
				}
				up, err := net.Dial("tcp", parts[1])
				if err != nil {
					_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
					return
				}
				defer up.Close()
				hits.Add(1)
				if _, err := conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
					return
				}
				go func() { _, _ = io.Copy(up, br) }()
				_, _ = io.Copy(conn, up)
			}()
		}
	}()
	return ln.Addr().String(), hits
}

func TestOpenWebSocketUsesUpstreamProxy(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, true)
	proxyAddr, hits := connectProxy(t)

	if err := app.Engine.SetUpstreamProxy("http://" + proxyAddr); err != nil {
		t.Fatal(err)
	}
	// SetUpstreamProxy 会写 capture 侧的包级变量,测试之间必须还原。
	t.Cleanup(func() { _ = app.Engine.SetUpstreamProxy("") })

	id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.CloseWebSocket(id) })
	if err := app.SendWSMessage(id, flow.WSText, "via proxy"); err != nil {
		t.Fatal(err)
	}
	if got := srv.nextFrame(t, srv.received, "经代理送达的帧"); string(got.data) != "via proxy" {
		t.Fatalf("服务端收到 %q", string(got.data))
	}
	if hits.Load() == 0 {
		t.Fatal("拨号未经过上游代理")
	}
}

func TestComposeWSProxyRejectsHTTPSScheme(t *testing.T) {
	app := newComposeApp(t)
	t.Cleanup(func() { _ = app.Engine.SetUpstreamProxy("") })

	if err := app.Engine.SetUpstreamProxy("https://proxy.test:8443"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.composeWSProxy(nil); err == nil {
		t.Fatal("https 上游代理应如实报错而不是静默直连")
	}

	if err := app.Engine.SetUpstreamProxy("socks5h://proxy.test:1080"); err != nil {
		t.Fatal(err)
	}
	u, err := app.composeWSProxy(nil)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.Scheme != "socks5" {
		t.Fatalf("socks5h 应归一为 socks5,实际 %v", u)
	}

	if err := app.Engine.SetUpstreamProxy(""); err != nil {
		t.Fatal(err)
	}
	if u, err := app.composeWSProxy(nil); err != nil || u != nil {
		t.Fatalf("未配置上游代理时应直连,实际 u=%v err=%v", u, err)
	}
}
