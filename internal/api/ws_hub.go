// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

// writeWait 是单次写入的上限:客户端卡住时不能让 writePump 永久阻塞,
// 否则 stop 关掉 send channel 也换不来连接真正断开。
const writeWait = 10 * time.Second

// Hub 是 headless 模式的 WebSocket 广播中心,把 service 事件实时推给所有前端客户端。
type Hub struct {
	svc        *service.Service
	clients    map[*wsClient]bool
	register   chan *wsClient
	unregister chan *wsClient
	started    atomic.Bool
	done       chan struct{} // 关闭表示要求 run 退出
	stopped    chan struct{} // 由 run 在退出前关闭
	stopOnce   sync.Once
}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

type wsEnvelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// 保留 gorilla 默认的同源校验。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func newHub(svc *service.Service) *Hub {
	return &Hub{
		svc:        svc,
		clients:    make(map[*wsClient]bool),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
		done:       make(chan struct{}),
		stopped:    make(chan struct{}),
	}
}

// start 启动广播循环,只应由 Serve 调用一次。
func (h *Hub) start() {
	h.started.Store(true)
	go h.run()
}

// stop 请求广播循环退出并断开所有已升级的连接,循环真正退出后才返回
// (ctx 到期则提前返回)。可重复调用。
func (h *Hub) stop(ctx context.Context) {
	h.stopOnce.Do(func() { close(h.done) })
	if !h.started.Load() {
		return
	}
	select {
	case <-h.stopped:
	case <-ctx.Done():
	}
}

// run 订阅事件总线并向所有客户端广播,直到 stop 被调用。
func (h *Hub) run() {
	// 退出顺序:先退订总线,再放行 stop 的等待方。
	defer close(h.stopped)
	events, cancel := h.svc.Bus().Subscribe()
	defer cancel()

	for {
		select {
		case <-h.done:
			for c := range h.clients {
				h.drop(c)
			}
			return
		case c := <-h.register:
			h.clients[c] = true
		case c := <-h.unregister:
			h.drop(c)
		case e := <-events:
			data, err := json.Marshal(translate(e))
			if err != nil {
				continue
			}
			for c := range h.clients {
				select {
				case c.send <- data:
				default:
					// 慢客户端:丢弃并移除。
					h.drop(c)
				}
			}
		}
	}
}

// drop 摘除客户端并关闭其发送 channel,writePump 据此发出 Close 帧并断开连接。
// 只在 run 的 goroutine 内调用,故对 clients 的读写无需加锁。
func (h *Hub) drop(c *wsClient) {
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
}

// translate 把引擎事件映射为前端期望的消息类型。
func translate(e core.Event) wsEnvelope {
	var t string
	switch e.Type {
	case core.EventFlowStarted:
		t = "http_request"
	case core.EventFlowCompleted:
		t = "http_response"
	case core.EventFlowUpdated:
		t = "session_updated"
	case core.EventWSMessage:
		t = "websocket_session"
	case core.EventStreamMessage:
		t = "stream_session"
	case core.EventBreakpointHit:
		t = "breakpoint_hit"
	case core.EventBreakpointResolved:
		t = "breakpoint_resolved"
	default:
		t = string(e.Type)
	}
	return wsEnvelope{Type: t, Payload: e.Payload}
}

// handleWS 升级 HTTP 连接为 WebSocket 并注册客户端。
func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	// Upgrade 自己也会拒绝非 GET,但它回的 405 不带 Allow,与其余端点的契约对不上。
	if !allowMethods(w, r, http.MethodGet) {
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &wsClient{conn: conn, send: make(chan []byte, 256)}
	select {
	case h.register <- c:
	case <-h.done:
		// 广播循环已停,没人会接管这条连接。
		_ = conn.Close()
		return
	}

	go h.writePump(c)
	h.readPump(c)
}

func (h *Hub) readPump(c *wsClient) {
	defer func() {
		select {
		case h.unregister <- c:
		case <-h.done:
		}
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(1 << 20)
	for {
		// 读取(并丢弃)客户端消息,仅用于检测连接关闭。
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *Hub) writePump(c *wsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
