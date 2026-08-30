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

// 本文件是跨端点的方法白名单与路径矩阵总表:每条路由 × 每种方法都要么被放行、要么回带 Allow 的 405,
// 且被拒的请求不得产生任何副作用。CLAUDE.md 的安全基线「变更动作结构上不可能被 GET/HEAD 到达」
// 就靠这几张表守住。

// allMethods 除标准方法外还带一个自造方法:HTTP 方法是任意 token,只照着标准方法写的判断
// 会放过 FOO 这类,矩阵必须把它算进去。
var allMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodOptions,
	http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, "FOO",
}

// serveMethod 用一台全新的、依赖齐全的服务器发一条请求,返回记录器与服务器本体
// (副作用断言要靠后者拿到 svc 与替身)。
func serveMethod(t *testing.T, method, path string) (*httptest.ResponseRecorder, *Server) {
	t.Helper()
	s, mux := newTestServer(t)
	return do(t, mux, method, path, "{}"), s
}

// TestPluginSourceMethodWhitelist /source 的读写共用一条路径,方法一旦放宽,本想保存的请求会落到
// 读分支,拿到 200 和旧源码,改动被静默丢弃。
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
			// 被拒的请求不能顺手把源码带出去。
			if strings.Contains(rec.Body.String(), "onRequest") {
				t.Errorf("405 响应不应含插件源码: %s", rec.Body.String())
			}
		})
	}
}

// TestPluginActionsRejectUnexpectedMethods 动作由路径段决定,方法一旦放宽,
// DELETE /api/plugins/{id}/enable 这种自相矛盾的请求会真的启用插件。
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

// TestPluginUnknownActionIs404 未知动作是路径问题,不是「服务器不支持该方法」,不能回 405/501。
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

// TestPluginExtraPathSegmentsRejected 多余路径段必须拒绝而不是忽略,否则拼错的路径会当成父动作执行并回 200。
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

// TestPluginsCollectionMethods 集合端点只认 GET/HEAD 读、POST 建。其余方法若落到读分支,
// 用 PUT 做「创建」的调用方只看状态码会以为成功了。
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
		// 方法关口必须在「插件子系统是否装配」之前生效:两者正交,
		// 装没装插件都不该改变「这个方法不被支持」这个结论。
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

// TestRuleExtraPathSegmentsRejected 多余路径段一旦被忽略就会退化成对父资源动手:
// DELETE /api/intercept/rules/{id}/typo 删掉的是父规则,PUT 则把它整条覆盖。
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

// TestRuleToggleMethodWhitelist toggle 只认 POST/PUT;放宽到 DELETE/PATCH 会让语义相反的请求也翻转开关。
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

// TestReadOnlyEndpointsRejectMutatingMethods 只读端点必须拒绝变更方法:DELETE 回 200 数据
// 会让调用方以为删成功了。
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
				// 被拒的变更方法一个字节的状态都不该动。
				if _, total := s.svc.Sessions(1, 50); total != 1 {
					t.Errorf("会话总数变成 %d", total)
				}
			})
		}
	}
}

// TestSessionsClearOnlyPOST 清空历史只能由 POST 触发:这是全站破坏性最强的一次调用,
// 方法关口是它唯一的门槛。断言必须看副作用 —— 把 ClearSessions() 挪到 allowMethods 之前,
// 被拒的 GET 依然回 405 + Allow: POST,只看状态码的用例照样绿,而用户整段抓包历史已被抹掉。
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

// routePaths 覆盖 routes() 注册的每一条路由,子树路由各取一个代表路径。
// 会话相关的代表路径统一用 id "abc",与 newTestServer 里预置的那条会话对上,
// 好让 TestHeadMatchesGet 真的走到命中分支。
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

// TestMethodNotAllowedIsSelfConsistent 全路由 × 全方法扫一遍,双向钉住 Allow:声明的方法必须真能进,
// 没声明的方法必须真被拒。手写 switch + default 的站点靠这条防止 Allow 与 case 分支各改各的,
// 新增端点漏写 Allow 也会在这里失败。
func TestMethodNotAllowedIsSelfConsistent(t *testing.T) {
	t.Parallel()
	for _, path := range routePaths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			var accepted, declared []string
			for _, m := range allMethods {
				rec, _ := serveMethod(t, m, path)
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
				// 一条路由放行了全部 8 种方法(含自造的 FOO)只可能是方法关口整个失效了,
				// 而这正是最该被抓住的回归 —— 旧写法在这里 continue,等于放它过去。
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

// TestRoutePathsCoverEveryRoute 把 routePaths 与 routes() 双向绑在一起。只做反向覆盖是不够的:
// 某条路由被改名或删除后,routePaths 里的旧路径会静默落到更宽的子树模式(如 /api/breakpoints/resume-all
// 落回 /api/breakpoints/),上面两条矩阵继续跑却在测另一个处理器,被改名端点的方法白名单从此无人看守。
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

// TestPathVariantsRejected 三处手写路径解析各用一套机制(Trim+Split / Contains / Cut),
// 尾斜杠、空段、'.'/'..'、控制字符这些变体必须逐条钉住,且父资源一律不得被改动。
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
		// 断点两处手写切分现行回 400 而不是基线里的 404,如实记录这处分叉。
		{http.MethodPost, "/api/breakpoints/abc/resume/junk", http.StatusBadRequest},
		{http.MethodPost, "/api/breakpoints/rules/abc/toggle/junk", http.StatusBadRequest},
		{http.MethodPost, "/api/breakpoints/resume-all/junk", http.StatusNotFound},
		// 控制字符与相对段:结论不必统一,但一律不能落到「对父资源动手」的分支上。
		{http.MethodDelete, "/api/intercept/rules/abc/%00", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			rule := s.svc.CreateRule(&service.InterceptRule{Name: "r1", Enabled: true})
			s.svc.RecordFlowCompleted(newFlowFixture("abc"))
			bpRule := s.pipe.Breakpoints().AddRuleWithEnabled("a.com", true, false, true)
			// 表里的 abc 是占位符,换成真实 id 才能验证「父资源没被动」。
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

// TestUnavailableSubsystemsMatrix 未装配的子系统对读方法回空清单、对变更方法回 501「本次构建没有它」。
// 这条回退策略与方法约束正交,但两者的先后顺序在各处理器里并不一致 —— 表里如实记录分叉:
// 断点侧只有列表类入口回退成空清单,其余一律 501;插件侧的方法关口则在 nil 判空之后。
func TestUnavailableSubsystemsMatrix(t *testing.T) {
	t.Parallel()
	type expectation struct {
		code int
		body string // 非空时断言 data 的原文
		msg  string
	}
	cases := []struct {
		name       string
		handler    func(*Server) http.HandlerFunc
		path       string
		read       expectation
		mutate     expectation
		mutMethod  string
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
		// 全局开关返回的是一个对象而不是清单,空清单在这里没有意义,故读写都回 501。
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

	// 单条插件的 nil 判空在方法关口之前:未装插件系统的构建里,连方法白名单都不会生效。
	// 记录这处分叉,免得有人把 501 改成 fallthrough,让 PATCH/FOO 直接进到删除分支。
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

	// 证书管理未装配时三条端点同样回 501。
	t.Run("证书管理未装配", func(t *testing.T) {
		t.Parallel()
		_, mux := newTestServer(t)
		for _, path := range []string{"/api/certificate/regenerate", "/api/certificate/export", "/api/certificate/import"} {
			rec := do(t, mux, http.MethodPost, path, "{}")
			if rec.Code != http.StatusNotImplemented {
				t.Errorf("%s 状态码 = %d,期望 501", path, rec.Code)
			}
			if got := decodeEnvelope(t, rec).Message; got != "certificate management unavailable" {
				t.Errorf("%s 的 message = %q", path, got)
			}
		}
	})
}

// TestHeadMatchesGet 凡 GET 读得到的路径,HEAD 必须同码且不写出响应体 —— 客户端拿 HEAD 探活是常规做法,
// 两者分叉会让探活结果与真实可读性对不上;HEAD 若把整个 body 写出去,大文件探活就变成了全量下载。
func TestHeadMatchesGet(t *testing.T) {
	t.Parallel()
	for _, path := range routePaths {
		if path == "/api/ws" {
			continue // WebSocket 握手只认 GET,HEAD 无意义
		}
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			// 让 /api/sessions/abc 这几条代表路径真的命中,否则比的只是 404 == 404。
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

	// httptest.ResponseRecorder 不像真实服务器那样丢弃 HEAD 的响应体,故这一条走真实往返。
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
