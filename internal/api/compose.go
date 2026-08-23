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

// maxComposeRequestBytes 是 /api/compose 请求体的上限。
//
// 请求体本身的上限在 app 边界(flow.MaxComposeBodyBytes) —— 桌面 Bridge 直连 SendRequest,
// 只有那里管得住所有调用方,超限也由那里给出带尺寸的文案。这道上限只负责不让 json.Decode
// 先把一个无限大的请求体读进内存,与出站帧的 maxComposeWSSendBytes 同职。
const maxComposeRequestBytes int64 = 8 << 20

// RequestSender 暴露「按给定内容发起一次请求」给 API,由 app 实现(见 App.SendRequest)。
type RequestSender interface {
	SendRequest(spec flow.RequestSpec) (string, error)
	// StopStream 主动结束一条构造器发起的流(SSE),返回是否命中进行中的流。
	StopStream(id string) bool
}

// SetRequestSender 装配请求构造器的发送入口,须在 Listen 前调用。未装配时 /api/compose 返回 503。
func (s *Server) SetRequestSender(rs RequestSender) { s.sender = rs }

// handleCompose 按请求体发起一次请求,立即返回新 flow 的 ID —— 往返是异步的,
// 响应经 /api/ws 的 flow_updated 推送,或稍后 GET /api/sessions/{id} 取。
// POST /api/compose
func (s *Server) handleCompose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.sender == nil {
		fail(w, http.StatusServiceUnavailable, "request sender unavailable")
		return
	}
	var spec flow.RequestSpec
	if !decodeLimitedJSON(w, r, maxComposeRequestBytes, &spec, "invalid request spec") {
		return
	}
	id, err := s.sender.SendRequest(spec)
	if err != nil {
		// URL 无法解析、协议不受支持一类全是客户端输入问题,归 400。
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"flowId": id})
}

// handleSessionCompose 返回一条已捕获请求的保真快照,供构造器预填。
// GET /api/sessions/{id}/compose
func (s *Server) handleSessionCompose(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid session id")
		return
	}
	seed, found := s.svc.ComposeSeed(id)
	if !found {
		fail(w, http.StatusNotFound, "session not found")
		return
	}
	ok(w, seed)
}

// handleComposeStream 驾驭一条构造器发起的流。
// POST /api/compose/{id}/stop
//
// 必须拒绝 GET/HEAD:token 为空的兜底路径下同源检查挡不住浏览器发起的顶层导航 / <img src>
// 一类 GET(Sec-Fetch-Site 可能是 none、Host 是回环、Origin 缺省),方法检查是唯一的关口。
func (s *Server) handleComposeStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.sender == nil {
		fail(w, http.StatusServiceUnavailable, "request sender unavailable")
		return
	}
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/compose/"), "/")
	if id == "" || action != "stop" {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	if !s.sender.StopStream(id) {
		fail(w, http.StatusNotFound, "stream not found")
		return
	}
	ok(w, map[string]bool{"stopped": true})
}
