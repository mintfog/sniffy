// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"net/http"
	"strings"
)

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page, pageSize := pageParams(r)
	list, total := s.svc.Sessions(page, pageSize)
	paginated(w, list, total, page, pageSize)
}

func (s *Server) handleClearSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.svc.ClearSessions()
	ok(w, nil)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id, isRaw := strings.CutSuffix(rest, "/body/raw"); isRaw {
		s.handleSessionBodyRaw(w, r, id)
		return
	}
	if id, isBody := strings.CutSuffix(rest, "/body"); isBody {
		s.handleSessionBody(w, r, id)
		return
	}
	id := rest
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid session id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sess, found := s.svc.Session(id)
		if !found {
			fail(w, http.StatusNotFound, "session not found")
			return
		}
		ok(w, sess)
	case http.MethodDelete:
		s.svc.DeleteSession(id)
		ok(w, nil)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleSessionBody 按需返回会话请求/响应体原始字节(base64+MIME),供前端预览图片等
// 二进制内容。GET /api/sessions/{id}/body?source=request|response(缺省 response)。
func (s *Server) handleSessionBody(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid session id")
		return
	}
	dto, found := s.svc.MessageBody(id, r.URL.Query().Get("source"))
	if !found {
		fail(w, http.StatusNotFound, "session not found")
		return
	}
	ok(w, dto)
}

// handleSessionBodyRaw 流式写出消息体原始字节(支持 Range),供音视频直接播放与大体积
// 内容下载。GET /api/sessions/{id}/body/raw?source=request|response(缺省 response)。
// 与 /body 的区别:不做 base64、不受预览上限约束,大体积响应体直接从落盘副本发出。
func (s *Server) handleSessionBodyRaw(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid session id")
		return
	}
	s.svc.ServeMessageBody(w, r, id, r.URL.Query().Get("source"))
}

func (s *Server) handleWSSessions(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageParams(r)
	list, total := s.svc.WSSessions(page, pageSize)
	paginated(w, list, total, page, pageSize)
}

func (s *Server) handleWSSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/websocket-sessions/")
	sess, found := s.svc.WSSession(id)
	if !found {
		fail(w, http.StatusNotFound, "session not found")
		return
	}
	ok(w, sess)
}

func (s *Server) handleStreamSessions(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageParams(r)
	list, total := s.svc.StreamSessions(page, pageSize)
	paginated(w, list, total, page, pageSize)
}

func (s *Server) handleStreamSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/stream-sessions/")
	sess, found := s.svc.StreamSession(id)
	if !found {
		fail(w, http.StatusNotFound, "session not found")
		return
	}
	ok(w, sess)
}
