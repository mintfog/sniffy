// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

// Hub 是 headless 模式的 WebSocket 广播中心,把 service 事件实时推给所有前端客户端。
type Hub struct {
	svc        *service.Service
	clients    map[*wsClient]bool
	register   chan *wsClient
	unregister chan *wsClient
}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

type wsEnvelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // 本地工具,放行所有来源
}

func newHub(svc *service.Service) *Hub {
	return &Hub{
		svc:        svc,
		clients:    make(map[*wsClient]bool),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
	}
}

// run 订阅事件总线并向所有客户端广播。
func (h *Hub) run() {
	events, cancel := h.svc.Bus().Subscribe()
	defer cancel()

	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
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
					delete(h.clients, c)
					close(c.send)
				}
			}
		}
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
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &wsClient{conn: conn, send: make(chan []byte, 256)}
	h.register <- c

	go h.writePump(c)
	h.readPump(c)
}

func (h *Hub) readPump(c *wsClient) {
	defer func() {
		h.unregister <- c
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
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
