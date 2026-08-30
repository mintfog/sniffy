// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// 本文件覆盖 authMiddleware 的凭证与来源校验；方法和路径白名单见 method_test.go。

// authProbe 在中间件内记录请求是否到达处理器。状态码与 called 一起断言，区分中间件拒绝和处理器响应。
type authProbe struct {
	http.Handler
	called *bool
}

func newAuthProbe(s *Server) authProbe {
	called := new(bool)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
	return authProbe{Handler: s.authMiddleware(inner), called: called}
}

// assert 同时校验放行标志、状态码和错误信封，确保调用方能得到稳定的鉴权结果。
func (p authProbe) assert(t *testing.T, rec *httptest.ResponseRecorder, allow bool, status int, msg string) {
	t.Helper()
	if allow {
		if !*p.called || rec.Code != http.StatusOK {
			t.Errorf("应放行,got code=%d called=%v", rec.Code, *p.called)
		}
		return
	}
	if *p.called {
		t.Error("被拒的请求不应透传到处理器")
	}
	if rec.Code != status {
		t.Errorf("状态码 = %d,期望 %d", rec.Code, status)
	}
	if got := decodeEnvelope(t, rec); got.Success || got.Message != msg {
		t.Errorf("响应 = success:%v message:%q,期望 success:false message:%q", got.Success, got.Message, msg)
	}
}

// TestAuthLoopbackHostMatrix token 为空时仅接受回环 Host；精确主机匹配阻断 DNS rebinding 对管理 API 的访问。
func TestAuthLoopbackHostMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host  string
		allow bool
	}{
		{"127.0.0.1:8888", true},
		{"localhost:8888", true},
		{"127.0.0.2:8888", true}, // 整个 127/8 都是回环
		{"[::1]:8888", true},
		{"[::ffff:127.0.0.1]:8888", true},
		{"::1", true}, // 无括号无端口的 IPv6 字面量:SplitHostPort 失败后按裸 host 解析

		{"", false}, // 无 Host 一律 fail-closed
		{"evil.example", false},
		{"localhost.evil.com", false},
		{"127.0.0.1.evil.com", false},
		{"notlocalhost:8888", false},
		{"0.0.0.0:8888", false}, // 未指定地址不是回环:它在所有网卡上可达
		{"[::]:8888", false},
		{"[::ffff:0.0.0.0]:8888", false},
		// 大小写主机名、带方括号的无端口 IPv6、末尾点 FQDN 按当前解析规则拒绝。
		{"LOCALHOST:8888", false},
		{"localhost.", false},
		{"[::1]", false},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			t.Parallel()
			p := newAuthProbe(&Server{})
			rec := do(t, p, http.MethodPost, "/api/status", "", withHost(c.host))
			p.assert(t, rec, c.allow, http.StatusForbidden, "cross-site request forbidden")
		})
	}
}

// TestAuthSameOriginHeaderMatrix Origin 与 Sec-Fetch-Site 为无 token 请求提供来源校验；Origin: null 和 file:// 归跨站。
func TestAuthSameOriginHeaderMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		host   string // 为空时用默认的 127.0.0.1:8888
		origin string
		site   string
		allow  bool
	}{
		{"无 Origin 无 Sec-Fetch-Site", "", "", "", true}, // 命令行脚本的常规形态
		{"Sec-Fetch-Site: none", "", "", "none", true},  // 地址栏直接打开
		{"Sec-Fetch-Site: same-origin", "", "", "same-origin", true},
		{"scheme 大小写不敏感", "", "HTTP://127.0.0.1:8888", "", true},
		// Host 比较使用 EqualFold，兼容浏览器保留的大写主机名。
		{"host 大小写不敏感", "localhost:8888", "http://LOCALHOST:8888", "", true},
		{"Origin 带路径仍只比 host", "", "http://127.0.0.1:8888/some/path", "", true},

		{"Origin: null", "", "null", "", false},
		{"Origin: file://", "", "file://", "", false},
		{"跨站 Origin", "", "http://evil.example", "", false},
		{"同机不同端口 Origin", "", "http://127.0.0.1:9999", "", false},
		{"跨站 Sec-Fetch-Site", "", "", "cross-site", false},
		{"同站不同源 Sec-Fetch-Site", "", "", "same-site", false},
		// Sec-Fetch-Site 按浏览器定义的精确值匹配，未知大小写、空白或多值均拒绝。
		{"大写变体不被认可", "", "", "Same-Origin", false},
		{"尾随空格不被认可", "", "", "same-origin ", false},
		{"多值不被认可", "", "", "same-origin, cross-site", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newAuthProbe(&Server{})
			var opts []reqOpt
			if c.host != "" {
				opts = append(opts, withHost(c.host))
			}
			if c.origin != "" {
				opts = append(opts, withHeader("Origin", c.origin))
			}
			if c.site != "" {
				opts = append(opts, withHeader("Sec-Fetch-Site", c.site))
			}
			rec := do(t, p, http.MethodPost, "/api/status", "", opts...)
			p.assert(t, rec, c.allow, http.StatusForbidden, "cross-site request forbidden")
		})
	}
}

// TestAuthBearerHeaderBoundaries token 使用定长常量时间比较；Authorization 末尾空白由 TrimSpace 清理，以兼容文件换行。
func TestAuthBearerHeaderBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		auth  string
		allow bool
	}{
		{"正确 token", "Bearer secret", true},
		{"方案名大小写不敏感", "BeArEr secret", true},
		{"多余空白被裁掉", "Bearer  secret", true},
		{"尾随换行被裁掉", "Bearer secret\n", true},

		{"空 Authorization", "", false},
		{"只有方案名", "Bearer", false},
		{"方案名后无分隔", "Bearersecret", false},
		{"方案名拼错", "Bearerx secret", false},
		{"制表符不算分隔", "Bearer\tsecret", false},
		{"空 token", "Bearer ", false},
		{"token 是期望值的前缀", "Bearer sec", false},
		{"token 是期望值加后缀", "Bearer secretx", false},
		{"token 大小写不同", "Bearer SECRET", false},
		{"非 Bearer 方案", "Token secret", false},
		{"Basic 方案", "Basic c2VjcmV0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newAuthProbe(&Server{token: "secret"})
			var opts []reqOpt
			if c.auth != "" {
				opts = append(opts, withHeader("Authorization", c.auth))
			}
			rec := do(t, p, http.MethodGet, "/api/status", "", opts...)
			p.assert(t, rec, c.allow, http.StatusUnauthorized, "unauthorized")
		})
	}
}

// TestAuthTokenModeSkipsSameOriginCheck token 模式仅验证 Bearer，适配使用 TLS 的远程管理 API。
func TestAuthTokenModeSkipsSameOriginCheck(t *testing.T) {
	t.Parallel()
	remote := []reqOpt{
		withHost("admin.example.com"),
		withHeader("Sec-Fetch-Site", "cross-site"),
		withHeader("Origin", "https://evil.example"),
	}
	for _, c := range []struct {
		name  string
		auth  string
		allow bool
	}{
		{"凭证正确即放行", "Bearer secret", true},
		{"凭证错误仍拒绝", "Bearer wrong", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newAuthProbe(&Server{token: "secret"})
			rec := do(t, p, http.MethodGet, "/api/status", "",
				append(slices.Clone(remote), withHeader("Authorization", c.auth))...)
			p.assert(t, rec, c.allow, http.StatusUnauthorized, "unauthorized")
		})
	}
}

// TestAuthQueryTokenOnlyOnExactWSPath query token 仅用于精确的 WebSocket 握手路径；URL 可能进入日志、历史记录与 Referer。
func TestAuthQueryTokenOnlyOnExactWSPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path  string
		allow bool
	}{
		{"/api/ws", true},

		{"/api/ws/", false},
		{"/api/ws/extra", false},
		{"/api/ws2", false},
		{"/API/WS", false},
		{"/api/status", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			t.Parallel()
			p := newAuthProbe(&Server{token: "secret"})
			rec := do(t, p, http.MethodGet, c.path, "", withQuery("token=secret"))
			p.assert(t, rec, c.allow, http.StatusUnauthorized, "unauthorized")
		})
	}

	// Authorization 出现时优先使用 Header，query token 不作兜底。
	t.Run("错误的头不被 query token 兜底", func(t *testing.T) {
		t.Parallel()
		p := newAuthProbe(&Server{token: "secret"})
		rec := do(t, p, http.MethodGet, "/api/ws", "",
			withQuery("token=secret"), withHeader("Authorization", "Bearer wrong"))
		p.assert(t, rec, false, http.StatusUnauthorized, "unauthorized")
	})
}

// TestAuthRejectionLeaksNothing 中间件包在整个 mux 外层，认证失败统一返回 401，路由与方法信息不会泄漏。
func TestAuthRejectionLeaksNothing(t *testing.T) {
	t.Parallel()
	certs := &fakeCertificateManager{exportData: []byte("ca-pem"), exportMIME: "application/x-pem-file"}
	s, mux := newTestServer(t, withToken("secret"), withCerts(certs))
	s.svc.RecordFlowCompleted(newFlowFixture("Flow-A"))
	h := s.authMiddleware(mux)

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/status"},
		{http.MethodGet, "/api/does-not-exist"},
		{http.MethodPost, "/api/sessions/clear"},
		{http.MethodPost, "/api/certificate/export"},
		// 路由存在但方法不匹配、路径不存在均在认证前返回同一 401。
		{http.MethodDelete, "/api/status"},
		{http.MethodGet, "/api/sessions/clear"},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := do(t, h, c.method, c.path, `{"format":"pem"}`)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("状态码 = %d,期望 401(路由存不存在、方法允不允许都不该在认证前泄漏)", rec.Code)
			}
			if got := bodyKeys(t, rec); !slices.Equal(got, []string{"message", "success", "timestamp"}) {
				t.Errorf("响应键 = %v,期望恰为 {message,success,timestamp}", got)
			}
			if got := decodeEnvelope(t, rec); got.Success || got.Message != "unauthorized" {
				t.Errorf("响应 = success:%v message:%q", got.Success, got.Message)
			}
			if allow := rec.Header().Get("Allow"); allow != "" {
				t.Errorf("401 不应带 Allow 头(那是一份方法白名单),got %q", allow)
			}
			if strings.Contains(rec.Body.String(), "secret") {
				t.Errorf("响应体不应回显 token: %s", rec.Body.String())
			}
		})
	}

	if _, total := s.svc.Sessions(1, 50); total != 1 {
		t.Errorf("未认证的 POST /api/sessions/clear 不该清空会话,剩余 %d 条", total)
	}
	assertNoCalls(t, certs.calls)
}
