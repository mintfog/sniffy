// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

// Server 是 headless HTTP + WebSocket 传输层,全部委托 service。
type Server struct {
	svc     *service.Service
	pipe    *pipeline.Pipeline
	plugins PluginProvider
	hub     *Hub
	httpSrv *http.Server
	addr    string
}

// PluginProvider 暴露插件列表/开关给 API(由 internal/plugin 实现,P3 接入)。
type PluginProvider interface {
	ListPlugins() []map[string]any
	EnablePlugin(id string, enabled bool) error
	GetPluginSource(id string) (string, bool)
	SavePluginSource(id, source string) error
	CreatePlugin(meta map[string]any, source string) (map[string]any, error)
	DeletePlugin(id string) error
	UpdateManifest(id string, patch map[string]any) error
	ClearPluginLogs(id string) error
}

// New 创建 API 服务器。pipe/plugins 可为 nil(对应能力降级)。
func New(svc *service.Service, pipe *pipeline.Pipeline, plugins PluginProvider, addr string) *Server {
	s := &Server{svc: svc, pipe: pipe, plugins: plugins, addr: addr}
	s.hub = newHub(svc)
	return s
}

// Start 启动 HTTP 服务器与 WS 广播。
func (s *Server) Start() error {
	go s.hub.run()

	mux := http.NewServeMux()
	s.routes(mux)
	s.httpSrv = &http.Server{
		Addr:         s.addr,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // WS 需要长连接
		IdleTimeout:  60 * time.Second,
	}
	return s.httpSrv.ListenAndServe()
}

// Stop 关闭服务器。
func (s *Server) Stop(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", s.handleStatus)

	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/clear", s.handleClearSessions)
	mux.HandleFunc("/api/sessions/", s.handleSession)

	mux.HandleFunc("/api/websocket-sessions", s.handleWSSessions)
	mux.HandleFunc("/api/websocket-sessions/", s.handleWSSession)

	mux.HandleFunc("/api/stream-sessions", s.handleStreamSessions)
	mux.HandleFunc("/api/stream-sessions/", s.handleStreamSession)

	mux.HandleFunc("/api/statistics", s.handleStatistics)

	mux.HandleFunc("/api/config", s.handleConfig)

	mux.HandleFunc("/api/recording/start", s.handleRecordingStart)
	mux.HandleFunc("/api/recording/stop", s.handleRecordingStop)
	mux.HandleFunc("/api/recording/status", s.handleRecordingStatus)

	mux.HandleFunc("/api/certificate/ca", s.handleGetCA)
	mux.HandleFunc("/api/certificate/ios-profile", s.handleIOSProfile)
	mux.HandleFunc("/api/certificate/regenerate", s.handleRegenerateCA)

	mux.HandleFunc("/api/intercept/rules", s.handleRules)
	mux.HandleFunc("/api/intercept/rules/", s.handleRule)
	mux.HandleFunc("/api/intercept/stats", s.handleRuleStats)
	mux.HandleFunc("/api/intercept/history", s.handleHistory)
	mux.HandleFunc("/api/intercept/history/clear", s.handleHistoryClear)

	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/api/plugins/", s.handlePlugin)

	mux.HandleFunc("/api/breakpoints", s.handleBreakpoints)
	mux.HandleFunc("/api/breakpoints/", s.handleBreakpoint)

	mux.HandleFunc("/api/export", s.handleExport)

	mux.HandleFunc("/api/ws", s.hub.handleWS)
}

// ---- 响应辅助 ----

type apiResponse struct {
	Data      any    `json:"data,omitempty"`
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
	Timestamp string `json:"timestamp"`
}

type paginatedResponse struct {
	Data     any  `json:"data"`
	Total    int  `json:"total"`
	Page     int  `json:"page"`
	PageSize int  `json:"pageSize"`
	HasNext  bool `json:"hasNext"`
	HasPrev  bool `json:"hasPrev"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, apiResponse{Data: data, Success: true, Timestamp: time.Now().Format(time.RFC3339)})
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiResponse{Success: false, Message: msg, Timestamp: time.Now().Format(time.RFC3339)})
}

func paginated(w http.ResponseWriter, data any, total, page, pageSize int) {
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data:     data,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		HasNext:  page*pageSize < total,
		HasPrev:  page > 1,
	})
}

func pageParams(r *http.Request) (page, pageSize int) {
	page, pageSize = 1, 50
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := r.URL.Query().Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pageSize = n
		}
	}
	return
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- 处理器 ----

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{
		"status":  "running",
		"version": "2.0.0",
		"uptime":  s.svc.UptimeSeconds(),
	})
}

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
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
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

func (s *Server) handleStatistics(w http.ResponseWriter, r *http.Request) {
	ok(w, s.svc.Statistics())
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ok(w, s.svc.Config())
	case http.MethodPut, http.MethodPost:
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		ok(w, s.svc.UpdateConfig(patch))
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRecordingStart(w http.ResponseWriter, r *http.Request) {
	s.svc.StartRecording()
	ok(w, map[string]any{"recording": true})
}

func (s *Server) handleRecordingStop(w http.ResponseWriter, r *http.Request) {
	s.svc.StopRecording()
	ok(w, map[string]any{"recording": false})
}

func (s *Server) handleRecordingStatus(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"recording": s.svc.IsRecording()})
}

func (s *Server) handleGetCA(w http.ResponseWriter, r *http.Request) {
	pem := s.svc.CertificatePEM()
	if len(pem) == 0 {
		fail(w, http.StatusInternalServerError, "certificate unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=sniffy-ca.crt")
	_, _ = w.Write(pem)
}

// handleIOSProfile 返回内嵌根证书的 iOS 配置描述文件,供 Safari 下载安装。
// MIME application/x-apple-aspen-config 触发 iOS 识别为描述文件。
func (s *Server) handleIOSProfile(w http.ResponseWriter, r *http.Request) {
	profile := s.svc.IOSMobileconfig()
	if len(profile) == 0 {
		fail(w, http.StatusInternalServerError, "certificate unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", "attachment; filename=sniffy.mobileconfig")
	_, _ = w.Write(profile)
}

func (s *Server) handleRegenerateCA(w http.ResponseWriter, r *http.Request) {
	// 重新生成需要重启引擎以重新加载 CA;v1 暂作提示。
	ok(w, map[string]any{"message": "请删除 ~/.sniffy 下的 CA 文件并重启以重新生成"})
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		page, pageSize := pageParams(r)
		all := s.svc.Rules()
		total := len(all)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		paginated(w, all[start:end], total, page, pageSize)
	case http.MethodPost:
		var rule service.InterceptRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		ok(w, s.svc.CreateRule(&rule))
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRule(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/intercept/rules/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	if len(parts) > 1 && parts[1] == "toggle" {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rule, found := s.svc.ToggleRule(id, body.Enabled)
		if !found {
			fail(w, http.StatusNotFound, "rule not found")
			return
		}
		ok(w, rule)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rule, found := s.svc.Rule(id)
		if !found {
			fail(w, http.StatusNotFound, "rule not found")
			return
		}
		ok(w, rule)
	case http.MethodPut:
		var rule service.InterceptRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		updated, found := s.svc.UpdateRule(id, &rule)
		if !found {
			fail(w, http.StatusNotFound, "rule not found")
			return
		}
		ok(w, updated)
	case http.MethodDelete:
		s.svc.DeleteRule(id)
		ok(w, nil)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRuleStats(w http.ResponseWriter, r *http.Request) {
	ok(w, s.svc.RuleStats())
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageParams(r)
	paginated(w, []any{}, 0, page, pageSize)
}

func (s *Server) handleHistoryClear(w http.ResponseWriter, r *http.Request) {
	ok(w, nil)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	list, _ := s.svc.Sessions(1, 100000)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=sessions.json")
	_ = json.NewEncoder(w).Encode(list)
}

// ---- 插件(P3 接入 PluginProvider) ----

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		if r.Method == http.MethodPost {
			fail(w, http.StatusNotImplemented, "plugins unavailable")
			return
		}
		ok(w, []any{})
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Manifest map[string]any `json:"manifest"`
			Source   string         `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		created, err := s.plugins.CreatePlugin(body.Manifest, body.Source)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		ok(w, created)
		return
	}
	ok(w, s.plugins.ListPlugins())
}

func (s *Server) handlePlugin(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		fail(w, http.StatusNotImplemented, "plugins unavailable")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid plugin id")
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	// DELETE /api/plugins/{id} 删除插件。
	if action == "" && r.Method == http.MethodDelete {
		if err := s.plugins.DeletePlugin(id); err != nil {
			fail(w, http.StatusNotFound, err.Error())
			return
		}
		ok(w, nil)
		return
	}
	switch action {
	case "enable":
		_ = s.plugins.EnablePlugin(id, true)
		ok(w, nil)
	case "disable":
		_ = s.plugins.EnablePlugin(id, false)
		ok(w, nil)
	case "manifest":
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.plugins.UpdateManifest(id, patch); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			fail(w, status, err.Error())
			return
		}
		ok(w, nil)
	case "logs":
		if err := s.plugins.ClearPluginLogs(id); err != nil {
			fail(w, http.StatusNotFound, err.Error())
			return
		}
		ok(w, nil)
	case "source":
		if r.Method == http.MethodPut {
			var body struct {
				Source string `json:"source"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				fail(w, http.StatusBadRequest, "invalid json")
				return
			}
			if err := s.plugins.SavePluginSource(id, body.Source); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			ok(w, nil)
			return
		}
		src, found := s.plugins.GetPluginSource(id)
		if !found {
			fail(w, http.StatusNotFound, "plugin not found")
			return
		}
		ok(w, map[string]any{"source": src})
	default:
		fail(w, http.StatusNotImplemented, "not implemented")
	}
}

// ---- 断点 ----

func (s *Server) handleBreakpoints(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		ok(w, []any{})
		return
	}
	if r.Method == http.MethodPost {
		// 设置全局"断在请求/响应"开关。
		var body struct {
			OnRequest  bool `json:"onRequest"`
			OnResponse bool `json:"onResponse"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.pipe.Breakpoints().SetGlobalBreak(body.OnRequest, body.OnResponse)
		ok(w, body)
		return
	}
	ok(w, s.pipe.Breakpoints().List())
}

func (s *Server) handleBreakpoint(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/breakpoints/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "resume":
		var edited *flow.Flow
		_ = json.NewDecoder(r.Body).Decode(&edited)
		if s.pipe.Breakpoints().Resume(id, edited) {
			ok(w, nil)
		} else {
			fail(w, http.StatusNotFound, "breakpoint not found")
		}
	case "abort":
		if s.pipe.Breakpoints().Abort(id) {
			ok(w, nil)
		} else {
			fail(w, http.StatusNotFound, "breakpoint not found")
		}
	default:
		fail(w, http.StatusNotFound, "unknown action")
	}
}
