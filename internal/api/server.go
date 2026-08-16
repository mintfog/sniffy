// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	svc      *service.Service
	pipe     *pipeline.Pipeline
	plugins  PluginProvider
	certs    CertificateManager
	hub      *Hub
	httpSrv  *http.Server
	addr     string
	token    string
	tlsCert  string
	tlsKey   string
	listener net.Listener
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

// CertificateManager 暴露根证书生命周期操作，由 app 实现持久化与运行时热切换。
type CertificateManager interface {
	RegenerateCA() (string, error)
	ExportCAAs(format, password string) ([]byte, string, error)
	// ImportCA 对客户端数据问题返回实现 InvalidInputError 的错误。
	ImportCA(data []byte, password string) (string, error)
}

// InvalidInputError 标记可安全映射为 HTTP 400 的证书导入错误。
type InvalidInputError interface {
	error
	InvalidInput() bool
}

// New 创建 API 服务器。pipe/plugins 可为 nil；token 为空时仅允许同源回环请求。
func New(svc *service.Service, pipe *pipeline.Pipeline, plugins PluginProvider, certs CertificateManager, addr, token string) *Server {
	s := &Server{svc: svc, pipe: pipe, plugins: plugins, certs: certs, addr: addr, token: token}
	s.hub = newHub(svc)
	return s
}

// SetTLS 配置管理 API 的 TLS 证书，须在 Listen 前调用。
func (s *Server) SetTLS(certFile, keyFile string) {
	s.tlsCert = certFile
	s.tlsKey = keyFile
}

// Listen 绑定监听地址并校验 TLS 配置。成功后须调用 Serve。
func (s *Server) Listen() error {
	mux := http.NewServeMux()
	s.routes(mux)
	s.httpSrv = &http.Server{
		Addr:         s.addr,
		Handler:      s.authMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // WS 需要长连接
		IdleTimeout:  60 * time.Second,
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	if s.tlsCert != "" && s.tlsKey != "" {
		cert, err := tls.LoadX509KeyPair(s.tlsCert, s.tlsKey)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("加载管理 API TLS 证书: %w", err)
		}
		s.httpSrv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		s.listener = tls.NewListener(ln, s.httpSrv.TLSConfig)
	} else {
		s.listener = ln
	}
	return nil
}

// Serve 在 Listen 创建的套接字上提供服务，直到 Stop 被调用。
func (s *Server) Serve() error {
	if s.listener == nil {
		return errors.New("api: Serve 前必须先成功调用 Listen")
	}
	go s.hub.run()
	return s.httpSrv.Serve(s.listener)
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
	mux.HandleFunc("/api/certificate/export", s.handleExportCA)
	mux.HandleFunc("/api/certificate/import", s.handleImportCA)
	mux.HandleFunc("/api/server-certs", s.handleServerCerts)

	mux.HandleFunc("/api/intercept/rules", s.handleRules)
	mux.HandleFunc("/api/intercept/rules/", s.handleRule)
	mux.HandleFunc("/api/intercept/stats", s.handleRuleStats)
	mux.HandleFunc("/api/intercept/history", s.handleHistory)
	mux.HandleFunc("/api/intercept/history/clear", s.handleHistoryClear)

	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/api/plugins/", s.handlePlugin)

	mux.HandleFunc("/api/breakpoints", s.handleBreakpoints)
	mux.HandleFunc("/api/breakpoints/global", s.handleBreakpointGlobal)
	mux.HandleFunc("/api/breakpoints/rules", s.handleBreakpointRules)
	mux.HandleFunc("/api/breakpoints/rules/", s.handleBreakpointRule)
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

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			if !sameOriginOK(r) {
				fail(w, http.StatusForbidden, "cross-site request forbidden")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !s.tokenOK(r) {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tokenOK(r *http.Request) bool {
	expect := []byte(s.token)
	if auth := r.Header.Get("Authorization"); len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") {
		return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(auth[7:])), expect) == 1
	}
	if r.URL.Path == "/api/ws" {
		return subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), expect) == 1
	}
	return false
}

func sameOriginOK(r *http.Request) bool {
	if !isLoopbackHostHeader(r.Host) {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func isLoopbackHostHeader(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.svc.StartRecording()
	ok(w, map[string]any{"recording": true})
}

func (s *Server) handleRecordingStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
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
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.certs == nil {
		fail(w, http.StatusNotImplemented, "certificate management unavailable")
		return
	}
	if _, err := s.certs.RegenerateCA(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, map[string]any{"message": "根证书已重新生成"})
}

const maxCAImportBytes int64 = 10 << 20

// handleExportCA 按请求的格式返回根 CA 文件。导出口令放在 JSON 请求体中,
// 避免 PKCS12 口令出现在 URL 与访问日志里。
func (s *Server) handleExportCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.certs == nil {
		fail(w, http.StatusNotImplemented, "certificate management unavailable")
		return
	}
	var body struct {
		Format   string `json:"format"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		failExportJSONDecode(w, err)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		failExportJSONDecode(w, err)
		return
	}
	format, filename, valid := caExportFile(body.Format)
	if !valid {
		fail(w, http.StatusBadRequest, "unsupported certificate format")
		return
	}
	if format == "p12" {
		if body.Password == "" {
			fail(w, http.StatusBadRequest, "password is required for PKCS12 export")
			return
		}
	}
	data, contentType, err := s.certs.ExportCAAs(format, body.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(data) == 0 {
		fail(w, http.StatusInternalServerError, "certificate export is empty")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func failExportJSONDecode(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		fail(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return
	}
	fail(w, http.StatusBadRequest, "invalid json")
}

func caExportFile(requested string) (format, filename string, valid bool) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "", "pem":
		return "pem", "sniffy-ca.pem", true
	case "crt":
		return "crt", "sniffy-ca.crt", true
	case "der":
		return "der", "sniffy-ca.der", true
	case "p12":
		return "p12", "sniffy-ca.p12", true
	default:
		return "", "", false
	}
}

// handleImportCA 接收 multipart/form-data 中的 file 与可选 password,导入并热切换根 CA。
func (s *Server) handleImportCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.certs == nil {
		fail(w, http.StatusNotImplemented, "certificate management unavailable")
		return
	}
	// 额外 1 MiB 留给 multipart 边界和表单字段;文件本身仍严格限制为 10 MiB。
	r.Body = http.MaxBytesReader(w, r.Body, maxCAImportBytes+(1<<20))
	err := r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(w, http.StatusRequestEntityTooLarge, "certificate file is too large")
		} else {
			fail(w, http.StatusBadRequest, "invalid multipart form")
		}
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "missing certificate file")
		return
	}
	defer file.Close()
	if header.Size > maxCAImportBytes {
		fail(w, http.StatusRequestEntityTooLarge, "certificate file is too large")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCAImportBytes+1))
	if err != nil {
		fail(w, http.StatusBadRequest, "failed to read certificate file")
		return
	}
	if int64(len(data)) > maxCAImportBytes {
		fail(w, http.StatusRequestEntityTooLarge, "certificate file is too large")
		return
	}
	pem, err := s.certs.ImportCA(data, r.FormValue("password"))
	if err != nil {
		var invalid InvalidInputError
		if errors.As(err, &invalid) && invalid.InvalidInput() {
			fail(w, http.StatusBadRequest, err.Error())
		} else {
			fail(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	ok(w, map[string]any{"certificatePEM": pem})
}

// handleServerCerts 管理按主机导入的服务端证书:GET 列表(不含私钥)、POST 导入、DELETE 删除。
func (s *Server) handleServerCerts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ok(w, s.svc.ServerCerts())
	case http.MethodPost, http.MethodPut:
		var body struct {
			CertPEM string `json:"certPEM"`
			KeyPEM  string `json:"keyPEM"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		dto, err := s.svc.ImportServerCert(body.CertPEM, body.KeyPEM)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		ok(w, dto)
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, http.StatusBadRequest, "missing id")
			return
		}
		s.svc.DeleteServerCert(id)
		ok(w, map[string]any{"deleted": id})
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
		if isSafeMethod(r.Method) {
			fail(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
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
	if isSafeMethod(r.Method) && action != "source" {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
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

type breakpointGlobalState struct {
	OnRequest  bool `json:"onRequest"`
	OnResponse bool `json:"onResponse"`
}

type breakpointRuleInput struct {
	URL        string `json:"url"`
	OnRequest  bool   `json:"onRequest"`
	OnResponse bool   `json:"onResponse"`
	Enabled    *bool  `json:"enabled"`
}

func decodeBreakpointJSON(r *http.Request, dst any, allowEmpty bool) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func (s *Server) handleBreakpoints(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		if r.Method == http.MethodGet {
			ok(w, []any{})
		} else {
			fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		ok(w, s.pipe.Breakpoints().List())
	case http.MethodPost:
		// 设置全局"断在请求/响应"开关。
		// 保留该入口以兼容已有客户端；新客户端应使用 /api/breakpoints/global。
		var body breakpointGlobalState
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		s.pipe.Breakpoints().SetGlobalBreak(body.OnRequest, body.OnResponse)
		ok(w, body)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleBreakpointGlobal 读取或设置全局请求/响应断点开关。
func (s *Server) handleBreakpointGlobal(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		return
	}
	bp := s.pipe.Breakpoints()
	switch r.Method {
	case http.MethodGet:
		onRequest, onResponse := bp.GlobalBreak()
		ok(w, breakpointGlobalState{OnRequest: onRequest, OnResponse: onResponse})
	case http.MethodPut, http.MethodPost:
		var body breakpointGlobalState
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		bp.SetGlobalBreak(body.OnRequest, body.OnResponse)
		ok(w, body)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleBreakpointRules 列出或新增 URL 断点规则。
func (s *Server) handleBreakpointRules(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		if r.Method == http.MethodGet {
			ok(w, []any{})
		} else {
			fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		}
		return
	}
	bp := s.pipe.Breakpoints()
	switch r.Method {
	case http.MethodGet:
		ok(w, bp.ListRules())
	case http.MethodPost:
		var body breakpointRuleInput
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if strings.TrimSpace(body.URL) == "" {
			fail(w, http.StatusBadRequest, "url is required")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		created := bp.AddRuleWithEnabled(body.URL, body.OnRequest, body.OnResponse, enabled)
		ok(w, created)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleBreakpointRule 读取、更新、启停或删除单条 URL 断点规则。
func (s *Server) handleBreakpointRule(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/breakpoints/rules/"), "/")
	parts := strings.Split(rest, "/")
	if rest == "" || len(parts) > 2 {
		fail(w, http.StatusBadRequest, "invalid breakpoint rule id")
		return
	}
	id := parts[0]
	bp := s.pipe.Breakpoints()

	if len(parts) == 2 {
		if parts[1] != "toggle" {
			fail(w, http.StatusNotFound, "unknown action")
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			fail(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Enabled == nil {
			fail(w, http.StatusBadRequest, "enabled is required")
			return
		}
		rule, found := bp.ToggleRule(id, *body.Enabled)
		if !found {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, rule)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rule, found := breakpointRuleByID(bp, id)
		if !found {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, rule)
	case http.MethodPut:
		var body breakpointRuleInput
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if strings.TrimSpace(body.URL) == "" {
			fail(w, http.StatusBadRequest, "url is required")
			return
		}
		rule, found := bp.UpdateRuleFields(id, body.URL, body.OnRequest, body.OnResponse, body.Enabled)
		if !found {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, rule)
	case http.MethodDelete:
		if !bp.DeleteRule(id) {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, nil)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func breakpointRuleByID(bp *pipeline.BreakpointManager, id string) (*pipeline.BreakRule, bool) {
	for _, rule := range bp.ListRules() {
		if rule.ID == id {
			return rule, true
		}
	}
	return nil, false
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
	if id == "" || len(parts) != 2 {
		fail(w, http.StatusBadRequest, "invalid breakpoint id")
		return
	}
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch action {
	case "resume":
		var edited *flow.Flow
		if err := decodeBreakpointJSON(r, &edited, true); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
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
