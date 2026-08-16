// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
func (p *spyPlugins) GetPluginSource(string) (string, bool)                       { return "", false }
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
