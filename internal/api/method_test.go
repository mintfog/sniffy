// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

// 本文件是跨端点的方法白名单与路径矩阵总表：每条路由按方法返回放行结果或带 Allow 的 405，
// 变更请求同时校验零副作用。

// allMethods 包含标准方法和自造方法，覆盖 HTTP 方法 token 的完整判断范围。
var allMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodOptions,
	http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, "FOO",
}

// serveMethod 用依赖齐全的新服务器发送请求；需要检查副作用的用例直接使用 newTestServer。
func serveMethod(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	_, mux := newTestServer(t)
	return do(t, mux, method, path, "{}")
}

// TestPluginSourceMethodWhitelist /source 读写共用路径，GET/HEAD 读取、PUT 保存，并返回稳定的 Allow 列表。
func TestPluginSourceMethodWhitelist(t *testing.T) {
	t.Parallel()
	const path = "/api/plugins/demo/source"
	allowed := []string{http.MethodGet, http.MethodHead, http.MethodPut}

	for _, m := range allMethods {
		if slices.Contains(allowed, m) {
			continue
		}
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			spy := &recordingPlugins{source: "function onRequest(f){}"}
			s := &Server{plugins: spy}
			rec := do(t, http.HandlerFunc(s.handlePlugin), m, path, `{"source":"new"}`)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("状态码 = %d,期望 405", rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD, PUT" {
				t.Errorf("Allow = %q,期望 \"GET, HEAD, PUT\"", got)
			}
			assertNoCalls(t, spy.calls)
			// 被拒响应不包含插件源码。
			if strings.Contains(rec.Body.String(), "onRequest") {
				t.Errorf("405 响应不应含插件源码: %s", rec.Body.String())
			}
		})
	}
}

// TestPluginActionsRejectUnexpectedMethods 动作由路径段和方法共同决定，意外方法返回 405 且不调用插件。
func TestPluginActionsRejectUnexpectedMethods(t *testing.T) {
	t.Parallel()
	cases := []struct {
		action  string
		allowed []string
		allow   string
	}{
		{"enable", []string{http.MethodPost}, "POST"},
		{"disable", []string{http.MethodPost}, "POST"},
		{"logs", []string{http.MethodPost}, "POST"},
		{"manifest", []string{http.MethodPost, http.MethodPut}, "POST, PUT"},
		{"", []string{http.MethodDelete}, "DELETE"},
	}
	for _, c := range cases {
		path := "/api/plugins/demo"
		if c.action != "" {
			path += "/" + c.action
		}
		for _, m := range allMethods {
			t.Run(fmt.Sprintf("%s %s", m, path), func(t *testing.T) {
				t.Parallel()
				spy := &recordingPlugins{}
				s := &Server{plugins: spy}
				rec := do(t, http.HandlerFunc(s.handlePlugin), m, path, "{}")

				if slices.Contains(c.allowed, m) {
					if rec.Code != http.StatusOK {
						t.Errorf("状态码 = %d,期望 200 (%s)", rec.Code, rec.Body.String())
					}
					return
				}
				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("状态码 = %d,期望 405", rec.Code)
				}
				if got := rec.Header().Get("Allow"); got != c.allow {
					t.Errorf("Allow = %q,期望 %q", got, c.allow)
				}
				assertNoCalls(t, spy.calls)
			})
		}
	}
}

// TestPluginEmptyIDRejected 空插件 ID 返回 400，插件管理器保持零调用。
func TestPluginEmptyIDRejected(t *testing.T) {
	t.Parallel()
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			spy := &recordingPlugins{}
			s := &Server{plugins: spy}
			rec := do(t, http.HandlerFunc(s.handlePlugin), m, "/api/plugins/", "{}")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d,期望 400", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "invalid plugin id" {
				t.Errorf("message = %q,期望 \"invalid plugin id\"", e.Message)
			}
			assertNoCalls(t, spy.calls)
		})
	}
}

// TestPluginUnknownActionIs404 未知动作按路径错误返回 404。
func TestPluginUnknownActionIs404(t *testing.T) {
	t.Parallel()
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			spy := &recordingPlugins{}
			s := &Server{plugins: spy}
			rec := do(t, http.HandlerFunc(s.handlePlugin), m, "/api/plugins/demo/bogus", "{}")
			if rec.Code != http.StatusNotFound {
				t.Errorf("状态码 = %d,期望 404", rec.Code)
			}
			assertNoCalls(t, spy.calls)
		})
	}
}

// TestPluginExtraPathSegmentsRejected 多余路径段返回 404，父动作和插件状态保持不变。
func TestPluginExtraPathSegmentsRejected(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/api/plugins/demo/enable/extra",
		"/api/plugins/demo/logs/x/y",
		"/api/plugins/demo/source/x",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			spy := &recordingPlugins{}
			s := &Server{plugins: spy}
			rec := do(t, http.HandlerFunc(s.handlePlugin), http.MethodPost, path, "{}")
			if rec.Code != http.StatusNotFound {
				t.Errorf("状态码 = %d,期望 404", rec.Code)
			}
			assertNoCalls(t, spy.calls)
		})
	}
}

// TestPluginsCollectionMethods 插件集合端点仅支持 GET/HEAD 读取和 POST 创建。
func TestPluginsCollectionMethods(t *testing.T) {
	t.Parallel()
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			s := &Server{plugins: &recordingPlugins{}}
			if rec := do(t, http.HandlerFunc(s.handlePlugins), m, "/api/plugins", ""); rec.Code != http.StatusOK {
				t.Errorf("状态码 = %d,期望 200", rec.Code)
			}
		})
	}
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, "FOO"} {
		// 方法白名单独立于插件子系统装配状态。
		for _, provider := range []PluginProvider{&recordingPlugins{}, nil} {
			t.Run(fmt.Sprintf("%s/%T", m, provider), func(t *testing.T) {
				t.Parallel()
				s := &Server{plugins: provider}
				rec := do(t, http.HandlerFunc(s.handlePlugins), m, "/api/plugins", "{}")
				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("状态码 = %d,期望 405", rec.Code)
				}
				if got := rec.Header().Get("Allow"); got != "GET, HEAD, POST" {
					t.Errorf("Allow = %q,期望 \"GET, HEAD, POST\"", got)
				}
			})
		}
	}
}

// TestRuleExtraPathSegmentsRejected 规则路径的多余段返回 404，父规则保持原值。
func TestRuleExtraPathSegmentsRejected(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		method string
		suffix string
	}{
		{http.MethodDelete, "/bogus"},
		{http.MethodPut, "/bogus"},
		{http.MethodGet, "/bogus"},
		{http.MethodPost, "/toggle/extra"},
	} {
		t.Run(c.method+c.suffix, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			rule := s.svc.CreateRule(&service.InterceptRule{Name: "r1", Enabled: true})

			path := fmt.Sprintf("/api/intercept/rules/%s%s", rule.ID, c.suffix)
			rec := do(t, mux, c.method, path, `{"name":"overwritten"}`)
			if rec.Code != http.StatusNotFound {
				t.Errorf("状态码 = %d,期望 404", rec.Code)
			}
			got, found := s.svc.Rule(rule.ID)
			if !found {
				t.Fatal("父规则被删掉了")
			}
			if got.Name != "r1" || !got.Enabled {
				t.Errorf("父规则被改动: name=%q enabled=%v", got.Name, got.Enabled)
			}
		})
	}
}

// TestRuleToggleMethodWhitelist toggle 仅支持 POST/PUT，其他方法返回 405 且保持开关状态。
func TestRuleToggleMethodWhitelist(t *testing.T) {
	t.Parallel()
	for _, m := range allMethods {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			rule := s.svc.CreateRule(&service.InterceptRule{Name: "r1", Enabled: true})

			path := fmt.Sprintf("/api/intercept/rules/%s/toggle", rule.ID)
			rec := do(t, mux, m, path, `{"enabled":false}`)
			got, _ := s.svc.Rule(rule.ID)

			if m == http.MethodPost || m == http.MethodPut {
				if rec.Code != http.StatusOK || got.Enabled {
					t.Errorf("toggle 应生效: code=%d enabled=%v", rec.Code, got.Enabled)
				}
				return
			}
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("状态码 = %d,期望 405", rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "POST, PUT" {
				t.Errorf("Allow = %q,期望 \"POST, PUT\"", allow)
			}
			if !got.Enabled {
				t.Error("被拒后不应改动开关")
			}
		})
	}
}

// TestReadOnlyEndpointsRejectMutatingMethods 只读端点对变更方法统一返回 405 和 Allow: GET, HEAD。
func TestReadOnlyEndpointsRejectMutatingMethods(t *testing.T) {
	t.Parallel()
	paths := []string{
		"/api/status",
		"/api/statistics",
		"/api/recording/status",
		"/api/websocket-sessions",
		"/api/websocket-sessions/abc",
		"/api/stream-sessions",
		"/api/stream-sessions/abc",
		"/api/certificate/ca",
		"/api/certificate/ios-profile",
	}
	for _, path := range paths {
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, "FOO"} {
			t.Run(m+" "+path, func(t *testing.T) {
				t.Parallel()
				s, mux := newTestServer(t)
				s.svc.RecordFlowCompleted(newFlowFixture("Flow-A"))

				rec := do(t, mux, m, path, "{}")
				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("状态码 = %d,期望 405", rec.Code)
				}
				if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
					t.Errorf("Allow = %q,期望 \"GET, HEAD\"", allow)
				}
				// 被拒的变更方法不改变会话存储。
				if _, total := s.svc.Sessions(1, 50); total != 1 {
					t.Errorf("会话总数变成 %d", total)
				}
			})
		}
	}
}

// TestSessionsClearOnlyPOST 清空历史仅由 POST 触发，其他方法返回 405 且保留会话。
func TestSessionsClearOnlyPOST(t *testing.T) {
	t.Parallel()
	for _, m := range allMethods {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			for _, id := range []string{"Flow-A", "Flow-B", "Flow-C"} {
				s.svc.RecordFlowCompleted(newFlowFixture(id))
			}

			rec := do(t, mux, m, "/api/sessions/clear", "")
			_, total := s.svc.Sessions(1, 50)

			if m == http.MethodPost {
				if rec.Code != http.StatusOK {
					t.Errorf("状态码 = %d,期望 200", rec.Code)
				}
				if total != 0 {
					t.Errorf("POST 后仍剩 %d 条会话", total)
				}
				return
			}
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("状态码 = %d,期望 405", rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "POST" {
				t.Errorf("Allow = %q,期望 \"POST\"", allow)
			}
			if total != 3 {
				t.Errorf("被拒的 %s 抹掉了会话历史,剩余 %d 条", m, total)
			}
		})
	}
}

// routePaths 覆盖 routes() 注册的每条路由，子树路由各取一个代表路径；会话代表 ID 统一为 abc。
var routePaths = []string{
	"/api/status",
	"/api/sessions",
	"/api/sessions/clear",
	"/api/sessions/abc",
	"/api/sessions/abc/body",
	"/api/sessions/abc/body/raw",
	"/api/sessions/abc/compose",
	"/api/compose",
	"/api/compose/abc/stop",
	"/api/compose/ws",
	"/api/compose/ws/abc/send",
	"/api/compose/ws/abc/close",
	"/api/websocket-sessions",
	"/api/websocket-sessions/abc",
	"/api/stream-sessions",
	"/api/stream-sessions/abc",
	"/api/statistics",
	"/api/config",
	"/api/recording/start",
	"/api/recording/stop",
	"/api/recording/status",
	"/api/certificate/ca",
	"/api/certificate/ios-profile",
	"/api/certificate/regenerate",
	"/api/certificate/export",
	"/api/certificate/import",
	"/api/server-certs",
	"/api/intercept/rules",
	"/api/intercept/rules/abc",
	"/api/intercept/rules/abc/toggle",
	"/api/plugins",
	"/api/plugins/demo",
	"/api/plugins/demo/enable",
	"/api/plugins/demo/disable",
	"/api/plugins/demo/manifest",
	"/api/plugins/demo/logs",
	"/api/plugins/demo/source",
	"/api/breakpoints",
	"/api/breakpoints/global",
	"/api/breakpoints/rules",
	"/api/breakpoints/rules/abc",
	"/api/breakpoints/rules/abc/toggle",
	"/api/breakpoints/resume-all",
	"/api/breakpoints/abort-all",
	"/api/breakpoints/abc/resume",
	"/api/export",
	"/api/ws",
}

// TestMethodNotAllowedIsSelfConsistent 扫描全路由与全方法，确保 Allow 声明和实际放行集合一致。
func TestMethodNotAllowedIsSelfConsistent(t *testing.T) {
	t.Parallel()
	for _, path := range routePaths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			var accepted, declared []string
			for _, m := range allMethods {
				rec := serveMethod(t, m, path)
				if rec.Code != http.StatusMethodNotAllowed {
					accepted = append(accepted, m)
					continue
				}
				allow := rec.Header().Get("Allow")
				if allow == "" {
					t.Errorf("%s 回了 405 却没有 Allow 头", m)
					continue
				}
				if declared == nil {
					declared = strings.Split(allow, ", ")
					continue
				}
				if allow != strings.Join(declared, ", ") {
					t.Errorf("对不同方法给出了不一致的 Allow: %q vs %q", allow, strings.Join(declared, ", "))
				}
			}
			if declared == nil {
				// 没有 405 表示该路由缺少方法白名单。
				t.Errorf("对全部 %d 种方法都未回 405,方法白名单可能整个失效(accepted=%v)", len(allMethods), accepted)
				return
			}
			sorted := slices.Clone(declared)
			slices.Sort(sorted)
			slices.Sort(accepted)
			if !slices.Equal(accepted, sorted) {
				t.Errorf("实际放行 %v,Allow 却声明 %v", accepted, declared)
			}
		})
	}
}

// patternRecorder 清点 routes() 注册了哪些模式。
type patternRecorder struct{ patterns []string }

func (p *patternRecorder) HandleFunc(pattern string, _ func(http.ResponseWriter, *http.Request)) {
	p.patterns = append(p.patterns, pattern)
}

// TestRoutePathsCoverEveryRoute 将 routePaths 与 routes() 双向校验，确保每条注册模式都有代表路径且路径均已注册。
func TestRoutePathsCoverEveryRoute(t *testing.T) {
	t.Parallel()
	rec := &patternRecorder{}
	(&Server{}).routes(rec)

	_, mux := newTestServer(t)
	matched := make([]string, 0, len(routePaths))
	for _, path := range routePaths {
		req := httptest.NewRequest(http.MethodGet, testHost+path, nil)
		_, pattern := mux.Handler(req)
		if pattern == "" {
			t.Errorf("routePaths 里的 %s 匹配不到任何注册模式", path)
			continue
		}
		matched = append(matched, pattern)
	}

	registered := slices.Clone(rec.patterns)
	slices.Sort(registered)
	slices.Sort(matched)
	matched = slices.Compact(matched)
	for _, pattern := range registered {
		if !slices.Contains(matched, pattern) {
			t.Errorf("routePaths 缺少命中 %s 的代表路径", pattern)
		}
	}
	for _, pattern := range matched {
		if !slices.Contains(registered, pattern) {
			t.Errorf("routePaths 命中了未注册的模式 %s(路由被改名或删除?)", pattern)
		}
	}
}

// TestPathVariantsRejected 覆盖多余路径段、尾斜杠和控制字符，验证父资源保持不变。
// 空段与相对段由 ServeMux cleanPath 重定向，会话侧空段由 sessions_test.go 直接覆盖。
func TestPathVariantsRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodDelete, "/api/sessions/abc/junk", http.StatusNotFound},
		{http.MethodDelete, "/api/sessions/abc/body/junk", http.StatusNotFound},
		{http.MethodGet, "/api/sessions/abc/junk/body", http.StatusNotFound},
		{http.MethodDelete, "/api/plugins/demo/junk", http.StatusNotFound},
		{http.MethodDelete, "/api/intercept/rules/abc/junk", http.StatusNotFound},
		{http.MethodPost, "/api/intercept/rules/abc/toggle/junk", http.StatusNotFound},
		// 断点 ID 解析错误返回 400，未知动作返回 404。
		{http.MethodPost, "/api/breakpoints/abc/resume/junk", http.StatusBadRequest},
		{http.MethodPost, "/api/breakpoints/rules/abc/toggle/junk", http.StatusBadRequest},
		{http.MethodPost, "/api/breakpoints/resume-all/junk", http.StatusNotFound},
		// 控制字符保持在错误分支，不进入父资源操作。
		{http.MethodDelete, "/api/intercept/rules/abc/%00", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			rule := s.svc.CreateRule(&service.InterceptRule{Name: "r1", Enabled: true})
			s.svc.RecordFlowCompleted(newFlowFixture("abc"))
			bpRule := s.pipe.Breakpoints().AddRuleWithEnabled("a.com", true, false, true)
			// 使用真实 ID，确保父资源状态断言有效。
			path := strings.ReplaceAll(c.path, "/rules/abc", "/rules/"+rule.ID)
			if strings.HasPrefix(c.path, "/api/breakpoints/rules/") {
				path = strings.ReplaceAll(c.path, "/rules/abc", "/rules/"+bpRule.ID)
			}

			if rec := do(t, mux, c.method, path, "{}"); rec.Code != c.want {
				t.Errorf("状态码 = %d,期望 %d", rec.Code, c.want)
			}
			if got, found := s.svc.Rule(rule.ID); !found || got.Name != "r1" || !got.Enabled {
				t.Errorf("拦截规则被动了: found=%v %+v", found, got)
			}
			if rules := s.pipe.Breakpoints().ListRules(); len(rules) != 1 || !rules[0].Enabled {
				t.Errorf("断点规则被动了: %+v", rules)
			}
			if _, found := s.svc.Session("abc"); !found {
				t.Error("父会话被删掉了")
			}
		})
	}
}

// TestTrailingSlashNormalization 记录尾斜杠在会话、规则、插件和断点规则端点的解析语义，
// 并验证父资源始终安全。
func TestTrailingSlashNormalization(t *testing.T) {
	t.Parallel()

	t.Run("会话侧拒绝尾斜杠", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		s.svc.RecordFlowCompleted(newFlowFixture("abc"))

		rec := do(t, mux, http.MethodDelete, "/api/sessions/abc/", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
		if _, found := s.svc.Session("abc"); !found {
			t.Error("被拒的请求删掉了父会话")
		}
	})

	t.Run("规则侧把尾斜杠归一成裸 id", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rule := s.svc.CreateRule(&service.InterceptRule{Name: "r1", Enabled: true})

		rec := do(t, mux, http.MethodDelete, "/api/intercept/rules/"+rule.ID+"/", "")
		if rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200(尾斜杠等同裸 id)", rec.Code)
		}
		if _, found := s.svc.Rule(rule.ID); found {
			t.Error("归一成裸 id 后应真的删掉该规则")
		}
	})

	t.Run("插件侧把尾斜杠归一成裸 id", func(t *testing.T) {
		t.Parallel()
		spy := &recordingPlugins{}
		s := &Server{plugins: spy}

		rec := do(t, http.HandlerFunc(s.handlePlugin), http.MethodDelete, "/api/plugins/demo/", "")
		if rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200(尾斜杠等同裸 id)", rec.Code)
		}
		assertCalls(t, spy.calls, call{Method: "DeletePlugin", Args: []any{"demo"}})
	})

	t.Run("断点规则侧把尾斜杠归一成裸 id", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rule := s.pipe.Breakpoints().AddRuleWithEnabled("a.com", true, false, true)

		// 归一成裸 ID 后按单条规则处理，POST 返回 405，DELETE 成功删除目标规则。
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/rules/"+rule.ID+"/", "{}")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("状态码 = %d,期望 405", rec.Code)
		}
		if got := do(t, mux, http.MethodDelete, "/api/breakpoints/rules/"+rule.ID+"/", ""); got.Code != http.StatusOK {
			t.Errorf("DELETE 状态码 = %d,期望 200", got.Code)
		}
		if n := len(s.pipe.Breakpoints().ListRules()); n != 0 {
			t.Errorf("归一成裸 id 后应真的删掉该规则,剩余 %d 条", n)
		}
	})
}

// TestUnavailableSubsystemsMatrix 未装配的子系统对列表读方法返回空清单，对对象和变更方法返回 501。
// 方法白名单与 nil 判空的顺序按各端点当前契约记录。
func TestUnavailableSubsystemsMatrix(t *testing.T) {
	t.Parallel()
	type expectation struct {
		code int
		body string // 非空时断言 data 的原文
		msg  string
	}
	cases := []struct {
		name      string
		handler   func(*Server) http.HandlerFunc
		path      string
		read      expectation
		mutate    expectation
		mutMethod string
	}{
		{"插件列表", func(s *Server) http.HandlerFunc { return s.handlePlugins }, "/api/plugins",
			expectation{code: http.StatusOK, body: "[]"},
			expectation{code: http.StatusNotImplemented, msg: "plugins unavailable"}, http.MethodPost},
		{"断点暂停清单", func(s *Server) http.HandlerFunc { return s.handleBreakpoints }, "/api/breakpoints",
			expectation{code: http.StatusOK, body: "[]"},
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"}, http.MethodPost},
		{"断点规则列表", func(s *Server) http.HandlerFunc { return s.handleBreakpointRules }, "/api/breakpoints/rules",
			expectation{code: http.StatusOK, body: "[]"},
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"}, http.MethodPost},
		// 全局开关返回对象，未装配时读写均返回 501。
		{"断点全局开关", func(s *Server) http.HandlerFunc { return s.handleBreakpointGlobal }, "/api/breakpoints/global",
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"},
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"}, http.MethodPut},
		{"断点单条规则", func(s *Server) http.HandlerFunc { return s.handleBreakpointRule }, "/api/breakpoints/rules/abc",
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"},
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"}, http.MethodDelete},
		{"断点批量放行", func(s *Server) http.HandlerFunc { return s.handleBreakpointResumeAll }, "/api/breakpoints/resume-all",
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"},
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"}, http.MethodPost},
		{"断点批量阻断", func(s *Server) http.HandlerFunc { return s.handleBreakpointAbortAll }, "/api/breakpoints/abort-all",
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"},
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"}, http.MethodPost},
		{"断点处置", func(s *Server) http.HandlerFunc { return s.handleBreakpoint }, "/api/breakpoints/abc/resume",
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"},
			expectation{code: http.StatusNotImplemented, msg: "breakpoints unavailable"}, http.MethodPost},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, _ := newTestServer(t, withoutPipeline(), withoutPlugins())
			h := http.HandlerFunc(c.handler(s))

			for _, m := range []string{http.MethodGet, http.MethodHead} {
				rec := do(t, h, m, c.path, "")
				if rec.Code != c.read.code {
					t.Errorf("%s 状态码 = %d,期望 %d", m, rec.Code, c.read.code)
				}
				if m == http.MethodGet && c.read.body != "" {
					if got := string(decodeEnvelope(t, rec).Data); got != c.read.body {
						t.Errorf("%s 的 data = %s,期望 %s", m, got, c.read.body)
					}
				}
				if m == http.MethodGet && c.read.msg != "" {
					if got := decodeEnvelope(t, rec).Message; got != c.read.msg {
						t.Errorf("%s 的 message = %q,期望 %q", m, got, c.read.msg)
					}
				}
			}

			rec := do(t, h, c.mutMethod, c.path, "{}")
			if rec.Code != c.mutate.code {
				t.Errorf("%s 状态码 = %d,期望 %d", c.mutMethod, rec.Code, c.mutate.code)
			}
			if got := decodeEnvelope(t, rec).Message; got != c.mutate.msg {
				t.Errorf("%s 的 message = %q,期望 %q", c.mutMethod, got, c.mutate.msg)
			}
		})
	}

	// 单条插件端点先执行 nil 判空，再处理方法白名单，因此返回 501 且没有 Allow 头。
	t.Run("单条插件的判空先于方法关口", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestServer(t, withoutPlugins())
		rec := do(t, http.HandlerFunc(s.handlePlugin), http.MethodPatch, "/api/plugins/demo", "{}")
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("状态码 = %d,期望 501", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "" {
			t.Errorf("501 不该带 Allow 头(方法关口尚未执行),got %q", got)
		}
	})

	// 证书管理端点由 certificate_test.go 的 TestCertificateManagementUnavailable 覆盖。
}

// TestHeadMatchesGet GET 可读路径的 HEAD 使用相同状态码并省略响应体，支持探活和大文件检查。
func TestHeadMatchesGet(t *testing.T) {
	t.Parallel()
	for _, path := range routePaths {
		if path == "/api/ws" {
			continue // WebSocket 握手只认 GET,HEAD 无意义
		}
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			// 为会话代表路径准备真实数据，验证端点分支。
			s.svc.RecordFlowCompleted(newFlowFixture("abc",
				withResponse(http.StatusOK, "audio/mpeg", []byte("0123456789"))))

			get := do(t, mux, http.MethodGet, path, "{}")
			if get.Code == http.StatusMethodNotAllowed {
				return
			}
			head := do(t, mux, http.MethodHead, path, "{}")
			if head.Code != get.Code {
				t.Errorf("HEAD 回 %d,GET 回 %d", head.Code, get.Code)
			}
		})
	}

	// 使用真实 HTTP 往返验证服务器对 HEAD 响应体的处理。
	t.Run("HEAD 不写出响应体", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		s.svc.RecordFlowCompleted(newFlowFixture("abc",
			withResponse(http.StatusOK, "audio/mpeg", []byte("0123456789"))))
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		for _, path := range []string{"/api/sessions/abc", "/api/sessions/abc/body", "/api/sessions/abc/body/raw"} {
			resp, err := srv.Client().Head(srv.URL + path)
			if err != nil {
				t.Fatalf("HEAD %s: %v", path, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("HEAD %s = %d,期望 200", path, resp.StatusCode)
			}
			if len(body) != 0 {
				t.Errorf("HEAD %s 写出了 %d 字节响应体", path, len(body))
			}
		}
	})
}
