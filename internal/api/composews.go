// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"net/http"
	"strings"

	"github.com/mintfog/sniffy/internal/flow"
)

// maxComposeWSSendBytes 是 /api/compose/ws/{id}/send 请求体的上限。
//
// 出站载荷的实际上限在 app 层(composeWSWriteLimit,8 MiB),这里留出的余量是编码开销:
// binary / ping 的 data 是 base64(约 4/3),再套一层 JSON 字符串转义。放宽到 12 MiB 后,
// 真正超限的帧仍由 app 层以「单帧载荷超过上限」拒绝,文案比一个笼统的 413 更有用;
// 这道上限只负责不让 json.Decode 先把一个无限大的请求体读进内存。

const maxComposeWSSendBytes int64 = 12 << 20

// WebSocketComposer 暴露「构造器发起并驾驭一条出站 WebSocket」给 API,由 app 实现。
// 不并入 RequestSender:后者是发一次拿 flowId 的无状态模型,这三个方法共同操作同一条
// 活着的连接,实现方须为此维护注册表。
type WebSocketComposer interface {
	OpenWebSocket(spec flow.RequestSpec) (string, error)
	SendWSMessage(flowID, msgType, data string) error
	CloseWebSocket(flowID string) error
}

// SetWebSocketComposer 装配构造器的出站 WebSocket 入口,须在 Listen 前调用。
// 未装配时 /api/compose/ws 一律返回 503。
func (s *Server) SetWebSocketComposer(c WebSocketComposer) { s.wsComposer = c }

// handleComposeWSOpen 建立一条出站 WebSocket,立即返回会话 ID;帧经 /api/ws 的 ws_message 推送。
// POST /api/compose/ws
//
// 与 handleCompose 一样只认 POST:无 token 的兜底路径下同源检查挡不住浏览器发起的
// 顶层导航 / <img src> 一类 GET,方法检查是唯一的关口。
func (s *Server) handleComposeWSOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.wsComposer == nil {
		fail(w, http.StatusServiceUnavailable, "websocket composer unavailable")
		return
	}
	var spec flow.RequestSpec
	if !decodeLimitedJSON(w, r, maxComposeRequestBytes, &spec, "invalid request spec") {
		return
	}
	id, err := s.wsComposer.OpenWebSocket(spec)
	if err != nil {
		// URL 无法解析、握手被拒一类全是客户端输入/目标站点问题,归 400。
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"flowId": id})
}

// composeWSSendBody 是 /api/compose/ws/{id}/send 的请求体。
// binary 与 ping 的 data 为 base64,text 为原文 —— 与回程 WSMessageDTO.Data 的编码约定对称。
type composeWSSendBody struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// handleComposeWSConn 驾驭一条已建立的出站连接。
// POST /api/compose/ws/{id}/send  body {"type":"text|binary|ping","data":"..."}
// POST /api/compose/ws/{id}/close
func (s *Server) handleComposeWSConn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.wsComposer == nil {
		fail(w, http.StatusServiceUnavailable, "websocket composer unavailable")
		return
	}
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/compose/ws/"), "/")
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid websocket id")
		return
	}
	switch action {
	case "send":
		var body composeWSSendBody
		if !decodeLimitedJSON(w, r, maxComposeWSSendBytes, &body, "invalid message body") {
			return
		}
		if err := s.wsComposer.SendWSMessage(id, body.Type, body.Data); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		ok(w, map[string]bool{"sent": true})
	case "close":
		if err := s.wsComposer.CloseWebSocket(id); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		ok(w, map[string]bool{"closed": true})
	default:
		fail(w, http.StatusNotFound, "not found")
	}
}
