// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package examples

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mintfog/sniffy/plugins"
)

// WebAPIPlugin Web API 服务插件
type WebAPIPlugin struct {
	*BasePlugin
	server           *http.Server
	mux              *http.ServeMux
	sessions         *SessionStorage
	statistics       *StatsCollector
	interceptRules   *RuleStorage
	interceptHistory *HistoryStorage
	// WebSocket 相关（预留，待实现）
	// wsMu             sync.RWMutex
	// wsClients        map[*WebSocketClient]bool
}

// SessionStorage 会话存储
type SessionStorage struct {
	httpSessions      map[string]*HTTPSession
	websocketSessions map[string]*WSSession
	mu                sync.RWMutex
}

// StatsCollector 统计信息收集器
type StatsCollector struct {
	totalRequests int64
	totalSessions int64
	totalBytes    int64
	statusCodes   map[int]int64
	methods       map[string]int64
	hosts         map[string]int64
	mu            sync.RWMutex
}

// RuleStorage 拦截规则存储
type RuleStorage struct {
	rules map[string]*InterceptRule
	mu    sync.RWMutex
}

// HistoryStorage 拦截历史存储
type HistoryStorage struct {
	records []InterceptHistoryRecord
	mu      sync.RWMutex
}

// WebSocketClient WebSocket客户端（预留，待实现）
// type WebSocketClient struct {
// 	conn chan []byte
// }

// HTTPSession HTTP会话
type HTTPSession struct {
	ID          string        `json:"id"`
	Request     HTTPRequest   `json:"request"`
	Response    *HTTPResponse `json:"response,omitempty"`
	Duration    int64         `json:"duration,omitempty"`
	Status      string        `json:"status"`
	Blocked     bool          `json:"blocked,omitempty"`
	Modified    bool          `json:"modified,omitempty"`
	ProcessName string        `json:"processName,omitempty"`
	ProcessID   int           `json:"processId,omitempty"`
}

// HTTPRequest HTTP请求
type HTTPRequest struct {
	ID        string            `json:"id"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body,omitempty"`
	Timestamp string            `json:"timestamp"`
	ClientIP  string            `json:"clientIP"`
	Host      string            `json:"host"`
	Path      string            `json:"path"`
	Protocol  string            `json:"protocol"`
	UserAgent string            `json:"userAgent,omitempty"`
}

// HTTPResponse HTTP响应
type HTTPResponse struct {
	ID           string            `json:"id"`
	RequestID    string            `json:"requestId"`
	Status       int               `json:"status"`
	StatusText   string            `json:"statusText"`
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body,omitempty"`
	Timestamp    string            `json:"timestamp"`
	Size         int64             `json:"size"`
	ResponseTime int64             `json:"responseTime"`
}

// WSSession WebSocket会话
type WSSession struct {
	ID           string      `json:"id"`
	URL          string      `json:"url"`
	Status       string      `json:"status"`
	StartTime    string      `json:"startTime"`
	EndTime      string      `json:"endTime,omitempty"`
	MessageCount int         `json:"messageCount"`
	TotalSize    int64       `json:"totalSize"`
	Messages     []WSMessage `json:"messages"`
	ProcessName  string      `json:"processName,omitempty"`
	ProcessID    int         `json:"processId,omitempty"`
}

// WSMessage WebSocket消息
type WSMessage struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Direction string `json:"direction"`
	Type      string `json:"type"`
	Data      string `json:"data"`
	Timestamp string `json:"timestamp"`
	Size      int64  `json:"size"`
}

// InterceptRule 拦截规则
type InterceptRule struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	Enabled       bool                 `json:"enabled"`
	Conditions    []InterceptCondition `json:"conditions"`
	Actions       []InterceptAction    `json:"actions"`
	Priority      int                  `json:"priority"`
	LogicOperator string               `json:"logicOperator"`
	Tags          []string             `json:"tags,omitempty"`
	CreatedAt     string               `json:"createdAt"`
	UpdatedAt     string               `json:"updatedAt"`
}

// InterceptCondition 拦截条件
type InterceptCondition struct {
	Type          string      `json:"type"`
	Operator      string      `json:"operator"`
	Value         interface{} `json:"value"`
	Value2        interface{} `json:"value2,omitempty"`
	CaseSensitive bool        `json:"caseSensitive,omitempty"`
	Negate        bool        `json:"negate,omitempty"`
	HeaderName    string      `json:"headerName,omitempty"`
}

// InterceptAction 拦截动作
type InterceptAction struct {
	Type        string                 `json:"type"`
	Parameters  map[string]interface{} `json:"parameters"`
	Enabled     bool                   `json:"enabled,omitempty"`
	Description string                 `json:"description,omitempty"`
}

// InterceptHistoryRecord 拦截历史记录
type InterceptHistoryRecord struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"sessionId"`
	RuleID    string                 `json:"ruleId"`
	RuleName  string                 `json:"ruleName"`
	Action    string                 `json:"action"`
	Timestamp string                 `json:"timestamp"`
	Details   map[string]interface{} `json:"details"`
}

// APIResponse 统一API响应格式
type APIResponse struct {
	Data      interface{} `json:"data,omitempty"`
	Success   bool        `json:"success"`
	Message   string      `json:"message,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// PaginatedResponse 分页响应
type PaginatedResponse struct {
	Data     interface{} `json:"data"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
	HasNext  bool        `json:"hasNext"`
	HasPrev  bool        `json:"hasPrev"`
}

// NewWebAPIPlugin 创建Web API插件
func NewWebAPIPlugin(api plugins.PluginAPI) plugins.Plugin {
	info := plugins.PluginInfo{
		Name:        "web_api",
		Version:     "1.0.0",
		Description: "提供Web管理界面的HTTP API服务",
		Author:      "sniffy",
		Category:    "api",
	}

	return &WebAPIPlugin{
		BasePlugin: NewBasePlugin(info, api),
		sessions: &SessionStorage{
			httpSessions:      make(map[string]*HTTPSession),
			websocketSessions: make(map[string]*WSSession),
		},
		statistics: &StatsCollector{
			statusCodes: make(map[int]int64),
			methods:     make(map[string]int64),
			hosts:       make(map[string]int64),
		},
		interceptRules: &RuleStorage{
			rules: make(map[string]*InterceptRule),
		},
		interceptHistory: &HistoryStorage{
			records: make([]InterceptHistoryRecord, 0),
		},
		// wsClients: make(map[*WebSocketClient]bool),
	}
}

// Start 启动插件
func (w *WebAPIPlugin) Start(ctx context.Context) error {
	if err := w.BasePlugin.Start(ctx); err != nil {
		return err
	}

	// 获取配置
	host := w.GetStringSetting("host", "0.0.0.0")
	port := w.GetIntSetting("port", 8888)

	// 创建路由
	w.mux = http.NewServeMux()
	w.setupRoutes()

	// 创建服务器
	addr := fmt.Sprintf("%s:%d", host, port)
	w.server = &http.Server{
		Addr:         addr,
		Handler:      w.corsMiddleware(w.mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 启动服务器
	go func() {
		w.logger.Info("Web API 服务器启动: http://%s", addr)
		if err := w.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			w.logger.Error("Web API 服务器错误: %v", err)
		}
	}()

	return nil
}

// Stop 停止插件
func (w *WebAPIPlugin) Stop(ctx context.Context) error {
	if w.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := w.server.Shutdown(shutdownCtx); err != nil {
			w.logger.Error("关闭 Web API 服务器失败: %v", err)
			return err
		}
		w.logger.Info("Web API 服务器已关闭")
	}

	return w.BasePlugin.Stop(ctx)
}

// setupRoutes 设置路由
func (w *WebAPIPlugin) setupRoutes() {
	// 系统状态
	w.mux.HandleFunc("/api/status", w.handleGetStatus)

	// 会话管理
	w.mux.HandleFunc("/api/sessions", w.handleSessions)
	w.mux.HandleFunc("/api/sessions/", w.handleSession)
	w.mux.HandleFunc("/api/sessions/clear", w.handleClearSessions)

	// WebSocket会话
	w.mux.HandleFunc("/api/websocket-sessions", w.handleWebSocketSessions)
	w.mux.HandleFunc("/api/websocket-sessions/", w.handleWebSocketSession)

	// 统计数据
	w.mux.HandleFunc("/api/statistics", w.handleGetStatistics)

	// 配置管理
	w.mux.HandleFunc("/api/config", w.handleConfig)

	// 录制控制
	w.mux.HandleFunc("/api/recording/start", w.handleStartRecording)
	w.mux.HandleFunc("/api/recording/stop", w.handleStopRecording)
	w.mux.HandleFunc("/api/recording/status", w.handleRecordingStatus)

	// 插件管理
	w.mux.HandleFunc("/api/plugins", w.handlePlugins)
	w.mux.HandleFunc("/api/plugins/", w.handlePlugin)

	// 导出功能
	w.mux.HandleFunc("/api/export", w.handleExport)

	// 证书管理
	w.mux.HandleFunc("/api/certificate/ca", w.handleGetCACertificate)
	w.mux.HandleFunc("/api/certificate/regenerate", w.handleRegenerateCertificate)

	// 拦截器管理
	w.mux.HandleFunc("/api/intercept/rules", w.handleInterceptRules)
	w.mux.HandleFunc("/api/intercept/rules/", w.handleInterceptRule)
	w.mux.HandleFunc("/api/intercept/stats", w.handleInterceptStats)
	w.mux.HandleFunc("/api/intercept/history", w.handleInterceptHistory)
	w.mux.HandleFunc("/api/intercept/history/clear", w.handleClearInterceptHistory)

	// WebSocket实时推送
	w.mux.HandleFunc("/api/ws", w.handleWebSocket)
}

// corsMiddleware CORS中间件
func (w *WebAPIPlugin) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		rw.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if req.Method == "OPTIONS" {
			rw.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(rw, req)
	})
}

// 响应辅助方法

// respondJSON 发送JSON响应
func (w *WebAPIPlugin) respondJSON(rw http.ResponseWriter, status int, data interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(data)
}

// respondSuccess 发送成功响应
func (w *WebAPIPlugin) respondSuccess(rw http.ResponseWriter, data interface{}) {
	w.respondJSON(rw, http.StatusOK, APIResponse{
		Data:      data,
		Success:   true,
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

// respondError 发送错误响应
func (w *WebAPIPlugin) respondError(rw http.ResponseWriter, status int, message string) {
	w.respondJSON(rw, status, APIResponse{
		Success:   false,
		Message:   message,
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

// respondPaginated 发送分页响应
func (w *WebAPIPlugin) respondPaginated(rw http.ResponseWriter, data interface{}, total, page, pageSize int) {
	hasNext := page*pageSize < total
	hasPrev := page > 1

	w.respondJSON(rw, http.StatusOK, PaginatedResponse{
		Data:     data,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		HasNext:  hasNext,
		HasPrev:  hasPrev,
	})
}

// 处理器方法

// handleGetStatus 获取系统状态
func (w *WebAPIPlugin) handleGetStatus(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.respondSuccess(rw, map[string]interface{}{
		"status":  "running",
		"version": "1.0.0",
		"uptime":  86400,
	})
}

// handleSessions 处理会话列表请求
func (w *WebAPIPlugin) handleSessions(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		w.handleGetSessions(rw, req)
	default:
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleGetSessions 获取会话列表
func (w *WebAPIPlugin) handleGetSessions(rw http.ResponseWriter, req *http.Request) {
	page := 1
	pageSize := 50

	// 解析查询参数
	if p := req.URL.Query().Get("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	if ps := req.URL.Query().Get("pageSize"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}

	w.sessions.mu.RLock()
	sessions := make([]*HTTPSession, 0, len(w.sessions.httpSessions))
	for _, session := range w.sessions.httpSessions {
		sessions = append(sessions, session)
	}
	w.sessions.mu.RUnlock()

	// 分页
	total := len(sessions)
	start := (page - 1) * pageSize
	end := start + pageSize

	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	paginatedSessions := sessions[start:end]
	w.respondPaginated(rw, paginatedSessions, total, page, pageSize)
}

// handleSession 处理单个会话请求
func (w *WebAPIPlugin) handleSession(rw http.ResponseWriter, req *http.Request) {
	// 提取会话ID
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/api/sessions/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		w.respondError(rw, http.StatusBadRequest, "Invalid session ID")
		return
	}
	sessionID := parts[0]

	switch req.Method {
	case http.MethodGet:
		w.handleGetSession(rw, req, sessionID)
	case http.MethodDelete:
		w.handleDeleteSession(rw, req, sessionID)
	default:
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleGetSession 获取单个会话
func (w *WebAPIPlugin) handleGetSession(rw http.ResponseWriter, req *http.Request, sessionID string) {
	w.sessions.mu.RLock()
	session, exists := w.sessions.httpSessions[sessionID]
	w.sessions.mu.RUnlock()

	if !exists {
		w.respondError(rw, http.StatusNotFound, "Session not found")
		return
	}

	w.respondSuccess(rw, session)
}

// handleDeleteSession 删除单个会话
func (w *WebAPIPlugin) handleDeleteSession(rw http.ResponseWriter, req *http.Request, sessionID string) {
	w.sessions.mu.Lock()
	delete(w.sessions.httpSessions, sessionID)
	w.sessions.mu.Unlock()

	w.respondSuccess(rw, nil)
}

// handleClearSessions 清空所有会话
func (w *WebAPIPlugin) handleClearSessions(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.sessions.mu.Lock()
	w.sessions.httpSessions = make(map[string]*HTTPSession)
	w.sessions.mu.Unlock()

	w.respondSuccess(rw, nil)
}

// handleWebSocketSessions 处理WebSocket会话列表
func (w *WebAPIPlugin) handleWebSocketSessions(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	page := 1
	pageSize := 50

	if p := req.URL.Query().Get("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	if ps := req.URL.Query().Get("pageSize"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}

	w.sessions.mu.RLock()
	sessions := make([]*WSSession, 0, len(w.sessions.websocketSessions))
	for _, session := range w.sessions.websocketSessions {
		sessions = append(sessions, session)
	}
	w.sessions.mu.RUnlock()

	// 分页
	total := len(sessions)
	start := (page - 1) * pageSize
	end := start + pageSize

	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	paginatedSessions := sessions[start:end]
	w.respondPaginated(rw, paginatedSessions, total, page, pageSize)
}

// handleWebSocketSession 处理单个WebSocket会话
func (w *WebAPIPlugin) handleWebSocketSession(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	sessionID := strings.TrimPrefix(req.URL.Path, "/api/websocket-sessions/")
	if sessionID == "" {
		w.respondError(rw, http.StatusBadRequest, "Invalid session ID")
		return
	}

	w.sessions.mu.RLock()
	session, exists := w.sessions.websocketSessions[sessionID]
	w.sessions.mu.RUnlock()

	if !exists {
		w.respondError(rw, http.StatusNotFound, "Session not found")
		return
	}

	w.respondSuccess(rw, session)
}

// handleGetStatistics 获取统计数据
func (w *WebAPIPlugin) handleGetStatistics(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.statistics.mu.RLock()
	defer w.statistics.mu.RUnlock()

	// 计算top hosts
	topHosts := make([]map[string]interface{}, 0)
	for host, count := range w.statistics.hosts {
		topHosts = append(topHosts, map[string]interface{}{
			"host":  host,
			"count": count,
		})
	}

	stats := map[string]interface{}{
		"totalRequests":          w.statistics.totalRequests,
		"totalSessions":          w.statistics.totalSessions,
		"totalBytes":             w.statistics.totalBytes,
		"requestsPerSecond":      0,
		"averageResponseTime":    0,
		"statusCodeDistribution": w.statistics.statusCodes,
		"methodDistribution":     w.statistics.methods,
		"topHosts":               topHosts,
	}

	w.respondSuccess(rw, stats)
}

// handleConfig 处理配置请求
func (w *WebAPIPlugin) handleConfig(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		w.handleGetConfig(rw, req)
	case http.MethodPut, http.MethodPost:
		w.handleUpdateConfig(rw, req)
	default:
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleGetConfig 获取配置
func (w *WebAPIPlugin) handleGetConfig(rw http.ResponseWriter, req *http.Request) {
	config := map[string]interface{}{
		"port":        8080,
		"host":        "0.0.0.0",
		"enableHTTPS": false,
		"plugins":     []interface{}{},
		"filters":     map[string]interface{}{},
		"recording":   true,
	}

	w.respondSuccess(rw, config)
}

// handleUpdateConfig 更新配置
func (w *WebAPIPlugin) handleUpdateConfig(rw http.ResponseWriter, req *http.Request) {
	var config map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&config); err != nil {
		w.respondError(rw, http.StatusBadRequest, "Invalid JSON")
		return
	}

	w.respondSuccess(rw, config)
}

// handleStartRecording 开始录制
func (w *WebAPIPlugin) handleStartRecording(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.respondSuccess(rw, nil)
}

// handleStopRecording 停止录制
func (w *WebAPIPlugin) handleStopRecording(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.respondSuccess(rw, nil)
}

// handleRecordingStatus 获取录制状态
func (w *WebAPIPlugin) handleRecordingStatus(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.respondSuccess(rw, map[string]any{
		"recording": true,
	})
}

// handlePlugins 处理插件列表
func (w *WebAPIPlugin) handlePlugins(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	plugins := []any{}
	w.respondSuccess(rw, plugins)
}

// handlePlugin 处理单个插件
func (w *WebAPIPlugin) handlePlugin(rw http.ResponseWriter, req *http.Request) {
	w.respondError(rw, http.StatusNotImplemented, "Not implemented")
}

// handleExport 处理导出
func (w *WebAPIPlugin) handleExport(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// 简单返回JSON格式的会话数据
	w.sessions.mu.RLock()
	sessions := make([]*HTTPSession, 0, len(w.sessions.httpSessions))
	for _, session := range w.sessions.httpSessions {
		sessions = append(sessions, session)
	}
	w.sessions.mu.RUnlock()

	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Content-Disposition", "attachment; filename=sessions.json")
	json.NewEncoder(rw).Encode(sessions)
}

// handleGetCACertificate 获取CA证书
func (w *WebAPIPlugin) handleGetCACertificate(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	certData := "-----BEGIN CERTIFICATE-----\nCA CERTIFICATE DATA\n-----END CERTIFICATE-----"
	rw.Header().Set("Content-Type", "application/x-pem-file")
	rw.Header().Set("Content-Disposition", "attachment; filename=ca.crt")
	rw.Write([]byte(certData))
}

// handleRegenerateCertificate 重新生成证书
func (w *WebAPIPlugin) handleRegenerateCertificate(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.respondSuccess(rw, nil)
}

// handleInterceptRules 处理拦截规则列表
func (w *WebAPIPlugin) handleInterceptRules(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		w.handleGetInterceptRules(rw, req)
	case http.MethodPost:
		w.handleCreateInterceptRule(rw, req)
	default:
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleGetInterceptRules 获取拦截规则列表
func (w *WebAPIPlugin) handleGetInterceptRules(rw http.ResponseWriter, req *http.Request) {
	page := 1
	pageSize := 50

	if p := req.URL.Query().Get("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	if ps := req.URL.Query().Get("pageSize"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}

	w.interceptRules.mu.RLock()
	rules := make([]*InterceptRule, 0, len(w.interceptRules.rules))
	for _, rule := range w.interceptRules.rules {
		rules = append(rules, rule)
	}
	w.interceptRules.mu.RUnlock()

	// 分页
	total := len(rules)
	start := (page - 1) * pageSize
	end := start + pageSize

	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	paginatedRules := rules[start:end]
	w.respondPaginated(rw, paginatedRules, total, page, pageSize)
}

// handleCreateInterceptRule 创建拦截规则
func (w *WebAPIPlugin) handleCreateInterceptRule(rw http.ResponseWriter, req *http.Request) {
	var rule InterceptRule
	if err := json.NewDecoder(req.Body).Decode(&rule); err != nil {
		w.respondError(rw, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// 生成ID和时间戳
	rule.ID = fmt.Sprintf("rule-%d", time.Now().UnixNano())
	rule.CreatedAt = time.Now().Format(time.RFC3339)
	rule.UpdatedAt = rule.CreatedAt

	w.interceptRules.mu.Lock()
	w.interceptRules.rules[rule.ID] = &rule
	w.interceptRules.mu.Unlock()

	w.respondSuccess(rw, rule)
}

// handleInterceptRule 处理单个拦截规则
func (w *WebAPIPlugin) handleInterceptRule(rw http.ResponseWriter, req *http.Request) {
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/api/intercept/rules/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		w.respondError(rw, http.StatusBadRequest, "Invalid rule ID")
		return
	}
	ruleID := parts[0]

	// 检查是否是toggle操作
	if len(parts) > 1 && parts[1] == "toggle" {
		w.handleToggleInterceptRule(rw, req, ruleID)
		return
	}

	switch req.Method {
	case http.MethodGet:
		w.handleGetInterceptRule(rw, req, ruleID)
	case http.MethodPut:
		w.handleUpdateInterceptRule(rw, req, ruleID)
	case http.MethodDelete:
		w.handleDeleteInterceptRule(rw, req, ruleID)
	default:
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleGetInterceptRule 获取单个拦截规则
func (w *WebAPIPlugin) handleGetInterceptRule(rw http.ResponseWriter, req *http.Request, ruleID string) {
	w.interceptRules.mu.RLock()
	rule, exists := w.interceptRules.rules[ruleID]
	w.interceptRules.mu.RUnlock()

	if !exists {
		w.respondError(rw, http.StatusNotFound, "Rule not found")
		return
	}

	w.respondSuccess(rw, rule)
}

// handleUpdateInterceptRule 更新拦截规则
func (w *WebAPIPlugin) handleUpdateInterceptRule(rw http.ResponseWriter, req *http.Request, ruleID string) {
	var updates map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&updates); err != nil {
		w.respondError(rw, http.StatusBadRequest, "Invalid JSON")
		return
	}

	w.interceptRules.mu.Lock()
	rule, exists := w.interceptRules.rules[ruleID]
	if !exists {
		w.interceptRules.mu.Unlock()
		w.respondError(rw, http.StatusNotFound, "Rule not found")
		return
	}

	// 更新时间戳
	rule.UpdatedAt = time.Now().Format(time.RFC3339)
	w.interceptRules.mu.Unlock()

	w.respondSuccess(rw, rule)
}

// handleDeleteInterceptRule 删除拦截规则
func (w *WebAPIPlugin) handleDeleteInterceptRule(rw http.ResponseWriter, req *http.Request, ruleID string) {
	w.interceptRules.mu.Lock()
	delete(w.interceptRules.rules, ruleID)
	w.interceptRules.mu.Unlock()

	w.respondSuccess(rw, nil)
}

// handleToggleInterceptRule 切换拦截规则状态
func (w *WebAPIPlugin) handleToggleInterceptRule(rw http.ResponseWriter, req *http.Request, ruleID string) {
	if req.Method != http.MethodPost {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var data struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
		w.respondError(rw, http.StatusBadRequest, "Invalid JSON")
		return
	}

	w.interceptRules.mu.Lock()
	rule, exists := w.interceptRules.rules[ruleID]
	if !exists {
		w.interceptRules.mu.Unlock()
		w.respondError(rw, http.StatusNotFound, "Rule not found")
		return
	}

	rule.Enabled = data.Enabled
	rule.UpdatedAt = time.Now().Format(time.RFC3339)
	w.interceptRules.mu.Unlock()

	w.respondSuccess(rw, rule)
}

// handleInterceptStats 获取拦截统计
func (w *WebAPIPlugin) handleInterceptStats(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.interceptRules.mu.RLock()
	totalRules := len(w.interceptRules.rules)
	activeRules := 0
	for _, rule := range w.interceptRules.rules {
		if rule.Enabled {
			activeRules++
		}
	}
	w.interceptRules.mu.RUnlock()

	w.interceptHistory.mu.RLock()
	totalInterceptions := len(w.interceptHistory.records)
	w.interceptHistory.mu.RUnlock()

	stats := map[string]interface{}{
		"totalRules":         totalRules,
		"activeRules":        activeRules,
		"totalInterceptions": totalInterceptions,
		"blockedRequests":    0,
		"modifiedRequests":   0,
		"modifiedResponses":  0,
	}

	w.respondSuccess(rw, stats)
}

// handleInterceptHistory 获取拦截历史
func (w *WebAPIPlugin) handleInterceptHistory(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	page := 1
	pageSize := 50

	if p := req.URL.Query().Get("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	if ps := req.URL.Query().Get("pageSize"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}

	w.interceptHistory.mu.RLock()
	history := make([]InterceptHistoryRecord, len(w.interceptHistory.records))
	copy(history, w.interceptHistory.records)
	w.interceptHistory.mu.RUnlock()

	// 分页
	total := len(history)
	start := (page - 1) * pageSize
	end := start + pageSize

	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	paginatedHistory := history[start:end]
	w.respondPaginated(rw, paginatedHistory, total, page, pageSize)
}

// handleClearInterceptHistory 清空拦截历史
func (w *WebAPIPlugin) handleClearInterceptHistory(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.respondError(rw, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.interceptHistory.mu.Lock()
	w.interceptHistory.records = make([]InterceptHistoryRecord, 0)
	w.interceptHistory.mu.Unlock()

	w.respondSuccess(rw, nil)
}

// handleWebSocket 处理WebSocket连接
func (w *WebAPIPlugin) handleWebSocket(rw http.ResponseWriter, req *http.Request) {
	// 这里需要实现WebSocket升级逻辑
	// 简化实现，实际需要使用WebSocket库
	w.respondError(rw, http.StatusNotImplemented, "WebSocket not implemented yet")
}

// 公开方法，供其他插件调用

// AddHTTPSession 添加HTTP会话
func (w *WebAPIPlugin) AddHTTPSession(session *HTTPSession) {
	w.sessions.mu.Lock()
	w.sessions.httpSessions[session.ID] = session
	w.sessions.mu.Unlock()

	// 更新统计信息
	w.statistics.mu.Lock()
	w.statistics.totalRequests++
	w.statistics.totalSessions++
	w.statistics.methods[session.Request.Method]++
	w.statistics.hosts[session.Request.Host]++
	if session.Response != nil {
		w.statistics.statusCodes[session.Response.Status]++
		w.statistics.totalBytes += session.Response.Size
	}
	w.statistics.mu.Unlock()
}

// AddWebSocketSession 添加WebSocket会话
func (w *WebAPIPlugin) AddWebSocketSession(session *WSSession) {
	w.sessions.mu.Lock()
	w.sessions.websocketSessions[session.ID] = session
	w.sessions.mu.Unlock()
}

// AddInterceptHistory 添加拦截历史记录
func (w *WebAPIPlugin) AddInterceptHistory(record InterceptHistoryRecord) {
	w.interceptHistory.mu.Lock()
	w.interceptHistory.records = append(w.interceptHistory.records, record)
	w.interceptHistory.mu.Unlock()
}

// InterceptRequest 实现请求拦截器接口，接收流量数据
func (w *WebAPIPlugin) InterceptRequest(ctx context.Context, interceptCtx *plugins.InterceptContext) (*plugins.InterceptResult, error) {
	// 将拦截的请求转换为 HTTPSession
	session := w.convertToHTTPSession(interceptCtx)

	// 添加到会话存储
	w.AddHTTPSession(session)

	w.logger.Debug("接收到新请求: %s %s", interceptCtx.Request.Method, interceptCtx.Request.URL.Path)

	return &plugins.InterceptResult{
		Continue: true,
		Modified: false,
	}, nil
}

// InterceptResponse 实现响应拦截器接口
func (w *WebAPIPlugin) InterceptResponse(ctx context.Context, interceptCtx *plugins.InterceptContext) (*plugins.InterceptResult, error) {
	// 更新已有会话的响应信息
	if interceptCtx.Response != nil {
		w.updateSessionResponse(interceptCtx)
		w.logger.Debug("接收到响应: %s %s - 状态码: %d",
			interceptCtx.Request.Method,
			interceptCtx.Request.URL.Path,
			interceptCtx.Response.StatusCode)
	}

	return &plugins.InterceptResult{
		Continue: true,
		Modified: false,
	}, nil
}

// convertToHTTPSession 将拦截上下文转换为 HTTPSession
func (w *WebAPIPlugin) convertToHTTPSession(interceptCtx *plugins.InterceptContext) *HTTPSession {
	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())

	// 转换请求头
	headers := make(map[string]string)
	for key, values := range interceptCtx.Request.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	// 获取请求头（使用 Get 方法，大小写不敏感）
	contentType := interceptCtx.Request.Header.Get("Content-Type")
	contentEncoding := interceptCtx.Request.Header.Get("Content-Encoding")

	// 处理请求体（检查是否需要解压）
	requestBody := processBody(interceptCtx.RequestBody, contentType, contentEncoding)

	// 创建请求对象
	request := HTTPRequest{
		ID:        requestID,
		Method:    interceptCtx.Request.Method,
		URL:       interceptCtx.Request.URL.String(),
		Headers:   headers,
		Body:      requestBody,
		Timestamp: time.Now().Format(time.RFC3339),
		ClientIP:  interceptCtx.Request.RemoteAddr,
		Host:      interceptCtx.Request.Host,
		Path:      interceptCtx.Request.URL.Path,
		Protocol:  interceptCtx.Request.Proto,
		UserAgent: interceptCtx.Request.UserAgent(),
	}

	session := &HTTPSession{
		ID:      sessionID,
		Request: request,
		Status:  "pending",
	}

	// TODO: 添加进程信息支持（需要从连接元数据中获取）
	// 可以在未来版本中通过 Connection 的元数据字段获取进程信息

	return session
}

// updateSessionResponse 更新会话的响应信息
func (w *WebAPIPlugin) updateSessionResponse(interceptCtx *plugins.InterceptContext) {
	// 查找对应的会话（通过URL和时间戳匹配）
	w.sessions.mu.Lock()
	defer w.sessions.mu.Unlock()

	// 简单实现：更新最近的匹配URL的会话
	url := interceptCtx.Request.URL.String()
	for _, session := range w.sessions.httpSessions {
		if session.Request.URL == url && session.Response == nil {
			// 转换响应头
			headers := make(map[string]string)
			for key, values := range interceptCtx.Response.Header {
				if len(values) > 0 {
					headers[key] = values[0]
				}
			}

			// 获取 Content-Type 和 Content-Encoding（使用 Get 方法，大小写不敏感）
			contentType := interceptCtx.Response.Header.Get("Content-Type")
			contentEncoding := interceptCtx.Response.Header.Get("Content-Encoding")

			// 调试日志
			w.logger.Debug("响应头 - Content-Type: %s, Content-Encoding: %s", contentType, contentEncoding)
			if contentEncoding != "" {
				w.logger.Info("检测到压缩内容: %s，原始大小: %d bytes", contentEncoding, len(interceptCtx.ResponseBody))
			}

			// 处理响应体（检查是否需要解压）
			responseBody := processBody(interceptCtx.ResponseBody, contentType, contentEncoding)

			// 创建响应对象
			responseID := fmt.Sprintf("res-%d", time.Now().UnixNano())
			session.Response = &HTTPResponse{
				ID:           responseID,
				RequestID:    session.Request.ID,
				Status:       interceptCtx.Response.StatusCode,
				StatusText:   interceptCtx.Response.Status,
				Headers:      headers,
				Body:         responseBody,
				Timestamp:    time.Now().Format(time.RFC3339),
				Size:         int64(len(interceptCtx.ResponseBody)),
				ResponseTime: time.Since(interceptCtx.Timestamp).Milliseconds(),
			}

			// 更新会话状态
			session.Status = "completed"
			session.Duration = time.Since(interceptCtx.Timestamp).Milliseconds()

			break
		}
	}
}

// processBody 处理响应体，根据内容类型判断是文本还是二进制
func processBody(body []byte, contentType, contentEncoding string) string {
	// 如果内容为空，直接返回
	if len(body) == 0 {
		return ""
	}

	originalSize := len(body)

	// 先尝试解压缩（如果是压缩内容）
	decompressedBody, wasCompressed := decompressBody(body, contentEncoding)
	if wasCompressed {
		body = decompressedBody
	} else if contentEncoding != "" {
		_ = originalSize
	}

	// 检查是否为二进制内容
	if containsBinaryData(body) {
		return fmt.Sprintf("<binary data, %d bytes>", len(body))
	}

	// 尝试转换为字符串
	bodyStr := string(body)

	// 验证是否为有效的 UTF-8 字符串
	if !utf8.ValidString(bodyStr) {
		return fmt.Sprintf("<binary data, %d bytes>", len(body))
	}

	// 如果内容太长，截断
	maxLength := 100000 // 100KB
	if len(bodyStr) > maxLength {
		return bodyStr[:maxLength] + fmt.Sprintf("\n... (truncated, total %d bytes)", len(body))
	}

	return bodyStr
}

// decompressBody 解压缩响应体
func decompressBody(body []byte, contentEncoding string) ([]byte, bool) {
	// 如果没有压缩编码，直接返回原内容
	if contentEncoding == "" {
		return body, false
	}

	contentEncoding = strings.ToLower(contentEncoding)

	// 处理 gzip 压缩
	if strings.Contains(contentEncoding, "gzip") {
		return decompressGzip(body)
	}

	// 处理 deflate 压缩
	if strings.Contains(contentEncoding, "deflate") {
		return decompressDeflate(body)
	}

	// 处理 brotli 压缩（br）
	if strings.Contains(contentEncoding, "br") {
		// Brotli 需要外部库，暂时返回原内容
		// 可以使用 github.com/andybalholm/brotli
		return body, false
	}

	// 不支持的压缩格式，返回原内容
	return body, false
}

// decompressGzip 解压 gzip 格式
func decompressGzip(body []byte) ([]byte, bool) {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		// 解压失败，返回原内容
		return body, false
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		// 读取失败，返回原内容
		return body, false
	}

	// 解压成功
	return decompressed, true
}

// decompressDeflate 解压 deflate 格式
func decompressDeflate(body []byte) ([]byte, bool) {
	reader := flate.NewReader(bytes.NewReader(body))
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return body, false
	}

	return decompressed, true
}

// containsBinaryData 检查字节数组是否包含二进制数据
func containsBinaryData(data []byte) bool {
	// 如果太短，检查全部
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512 // 只检查前 512 字节
	}

	// 计算非打印字符的比例
	nonPrintable := 0
	for i := 0; i < checkLen; i++ {
		b := data[i]
		// 控制字符（除了常见的如 \n, \r, \t）
		if b < 32 && b != 9 && b != 10 && b != 13 {
			nonPrintable++
		}
		// 高位字符
		if b > 126 && b < 128 {
			nonPrintable++
		}
	}

	// 如果超过 30% 是非打印字符，认为是二进制
	return float64(nonPrintable)/float64(checkLen) > 0.3
}

// 确保实现了Plugin和拦截器接口
var _ plugins.Plugin = (*WebAPIPlugin)(nil)
var _ plugins.RequestInterceptor = (*WebAPIPlugin)(nil)
var _ plugins.ResponseInterceptor = (*WebAPIPlugin)(nil)
