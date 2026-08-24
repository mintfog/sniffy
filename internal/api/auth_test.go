// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

func authProbe(s *Server) (http.Handler, *bool) {
	called := new(bool)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
	return s.authMiddleware(inner), called
}

func localReq(method string) *http.Request {
	return httptest.NewRequest(method, "http://127.0.0.1:8888/api/status", nil)
}

func TestAuthNoTokenAllowsLocalScript(t *testing.T) {
	h, called := authProbe(&Server{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, localReq(http.MethodPost))
	if !*called || rec.Code != http.StatusOK {
		t.Fatalf("回环脚本请求应放行,got code=%d called=%v", rec.Code, *called)
	}
}

func TestAuthNoTokenBlocksCSRF(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *http.Request)
	}{
		{"跨站 Sec-Fetch-Site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"same-site Sec-Fetch-Site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }},
		{"外部 Origin", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }},
		{"同机不同端口 Origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:9999") }},
		{"DNS rebinding: 非回环 Host", func(r *http.Request) { r.Host = "evil.example" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, called := authProbe(&Server{})
			req := localReq(http.MethodPost)
			c.setup(req)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if *called || rec.Code != http.StatusForbidden {
				t.Fatalf("期望 403 且不透传,got code=%d called=%v", rec.Code, *called)
			}
		})
	}
}

func TestAuthNoTokenAllowsSameOrigin(t *testing.T) {
	h, called := authProbe(&Server{})
	req := localReq(http.MethodPost)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "http://127.0.0.1:8888")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !*called || rec.Code != http.StatusOK {
		t.Fatalf("同源请求应放行,got code=%d called=%v", rec.Code, *called)
	}
}

func TestRecordingRequiresPOST(t *testing.T) {
	for _, h := range []http.HandlerFunc{(&Server{}).handleRecordingStart, (&Server{}).handleRecordingStop} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8888/api/recording/start", nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET 触发录制变更应返回 405,got %d", rec.Code)
		}
	}
}

type spyPlugins struct {
	enableCalled bool
	logsCleared  bool
}

func (p *spyPlugins) ListPlugins() []map[string]any { return nil }
func (p *spyPlugins) EnablePlugin(string, bool) error {
	p.enableCalled = true
	return nil
}
func (p *spyPlugins) GetPluginSource(string) (string, error)                      { return "", os.ErrNotExist }
func (p *spyPlugins) SavePluginSource(string, string) error                       { return nil }
func (p *spyPlugins) CreatePlugin(map[string]any, string) (map[string]any, error) { return nil, nil }
func (p *spyPlugins) DeletePlugin(string) error                                   { return nil }
func (p *spyPlugins) UpdateManifest(string, map[string]any) error                 { return nil }
func (p *spyPlugins) ClearPluginLogs(string) error {
	p.logsCleared = true
	return nil
}

func TestPluginMutationsRejectGET(t *testing.T) {
	for _, action := range []string{"enable", "disable", "logs"} {
		spy := &spyPlugins{}
		s := &Server{plugins: spy}
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8888/api/plugins/demo/"+action, nil)
		rec := httptest.NewRecorder()
		s.handlePlugin(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s 应返回 405,got %d", action, rec.Code)
		}
		if spy.enableCalled || spy.logsCleared {
			t.Fatalf("GET %s 不应触发副作用: enable=%v logs=%v", action, spy.enableCalled, spy.logsCleared)
		}
	}
}

func TestRuleToggleRejectsGET(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8888/api/intercept/rules/abc/toggle", nil)
	rec := httptest.NewRecorder()
	s.handleRule(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 触发规则 toggle 应返回 405,got %d", rec.Code)
	}
}

func TestAuthTokenRejects(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *http.Request)
	}{
		{"无凭证", func(r *http.Request) {}},
		{"Bearer 错误", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }},
		{"非 Bearer 方案", func(r *http.Request) { r.Header.Set("Authorization", "Basic c2VjcmV0") }},
		{"REST 上的 query token", func(r *http.Request) { r.URL.RawQuery = "token=secret" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, called := authProbe(&Server{token: "secret"})
			req := localReq(http.MethodGet)
			c.setup(req)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if *called || rec.Code != http.StatusUnauthorized {
				t.Fatalf("期望 401 且不透传,got code=%d called=%v", rec.Code, *called)
			}
		})
	}
}

func TestAuthTokenAccepts(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		setup  func(r *http.Request)
	}{
		{"Bearer 头", http.MethodGet, "http://127.0.0.1:8888/api/status",
			func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }},
		{"Bearer 大小写不敏感", http.MethodGet, "http://127.0.0.1:8888/api/status",
			func(r *http.Request) { r.Header.Set("Authorization", "bearer secret") }},
		{"WS 端点接受 query token", http.MethodGet, "http://127.0.0.1:8888/api/ws?token=secret",
			func(r *http.Request) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, called := authProbe(&Server{token: "secret"})
			req := httptest.NewRequest(c.method, c.path, nil)
			c.setup(req)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if !*called || rec.Code != http.StatusOK {
				t.Fatalf("期望放行,got code=%d called=%v", rec.Code, *called)
			}
		})
	}
}

// spyComposer 记录被调到的方法,用于断言「拒绝的请求不产生副作用」。
type spyComposer struct {
	opened  bool
	sent    bool
	closed  bool
	stopped bool
}

func (c *spyComposer) SendRequest(flow.RequestSpec) (string, error) { return "flow-1", nil }
func (c *spyComposer) StopStream(string) bool {
	c.stopped = true
	return true
}
func (c *spyComposer) OpenWebSocket(flow.RequestSpec) (string, error) {
	c.opened = true
	return "ws-1", nil
}
func (c *spyComposer) SendWSMessage(string, string, string) error {
	c.sent = true
	return nil
}
func (c *spyComposer) CloseWebSocket(string) error {
	c.closed = true
	return nil
}

func TestComposeWSRejectsGET(t *testing.T) {
	for _, path := range []string{
		"/api/compose/ws",
		"/api/compose/ws/x/send",
		"/api/compose/ws/x/close",
		"/api/compose/x/stop",
	} {
		spy := &spyComposer{}
		s := &Server{sender: spy, wsComposer: spy}
		mux := http.NewServeMux()
		s.routes(mux)
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8888"+path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s 应返回 405,got %d", path, rec.Code)
		}
		if spy.opened || spy.sent || spy.closed || spy.stopped {
			t.Fatalf("GET %s 不应触发副作用: %+v", path, spy)
		}
	}
}

func TestComposeWSUnavailableWithoutComposer(t *testing.T) {
	mux := http.NewServeMux()
	(&Server{}).routes(mux)
	for _, path := range []string{"/api/compose/ws", "/api/compose/ws/x/send", "/api/compose/ws/x/close"} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8888"+path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("未装配 composer 时 POST %s 应返回 503,got %d", path, rec.Code)
		}
	}
}

// TestComposeRoutePrecedence 钉住 ServeMux 的最长前缀匹配:/api/compose/ws 这一支
// 必须由 WebSocket handler 接管,不能被 /api/compose/ 的子树当成 id 为 "ws" 的流。
func TestComposeRoutePrecedence(t *testing.T) {
	cases := []struct {
		path    string
		wantWS  bool
		checker func(*spyComposer) bool
	}{
		{"/api/compose/ws", true, func(c *spyComposer) bool { return c.opened }},
		{"/api/compose/ws/abc/send", true, func(c *spyComposer) bool { return c.sent }},
		{"/api/compose/ws/abc/close", true, func(c *spyComposer) bool { return c.closed }},
		{"/api/compose/abc/stop", false, func(c *spyComposer) bool { return c.stopped }},
	}
	for _, c := range cases {
		spy := &spyComposer{}
		s := &Server{sender: spy, wsComposer: spy}
		mux := http.NewServeMux()
		s.routes(mux)
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8888"+c.path, strings.NewReader(`{"type":"text","data":"hi"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s 期望 200,got %d body=%s", c.path, rec.Code, rec.Body.String())
		}
		if !c.checker(spy) {
			t.Fatalf("POST %s 未路由到预期 handler: %+v", c.path, spy)
		}
		if c.wantWS && spy.stopped {
			t.Fatalf("POST %s 被 /api/compose/ 子树抢走", c.path)
		}
	}
}
