// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

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
