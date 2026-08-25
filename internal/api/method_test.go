// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

// allMethods 除标准方法外还带一个自造方法:HTTP 方法是任意 token,只照着标准方法写的判断
// 会放过 FOO 这类,矩阵必须把它算进去。
var allMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodOptions,
	http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, "FOO",
}

// methodSpyPlugins 在 spyPlugins 之上记下每一种变更是否真的发生,用于断言被拒的方法零副作用。
type methodSpyPlugins struct {
	spyPlugins
	savedSource     string
	saved           bool
	manifestUpdated bool
	deleted         bool
	sourceRead      bool
}

func (p *methodSpyPlugins) ListPlugins() []map[string]any { return []map[string]any{{"id": "demo"}} }

func (p *methodSpyPlugins) GetPluginSource(string) (string, error) {
	p.sourceRead = true
	return "function onRequest(f){}", nil
}

func (p *methodSpyPlugins) SavePluginSource(_, source string) error {
	p.saved, p.savedSource = true, source
	return nil
}

func (p *methodSpyPlugins) UpdateManifest(string, map[string]any) error {
	p.manifestUpdated = true
	return nil
}

func (p *methodSpyPlugins) DeletePlugin(string) error {
	p.deleted = true
	return nil
}

func (p *methodSpyPlugins) touched() bool {
	return p.enableCalled || p.logsCleared || p.saved || p.manifestUpdated || p.deleted || p.sourceRead
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// wiredServer 装配一台依赖齐全的服务器,让方法矩阵能走真实 mux 而不是直调处理器。
func wiredServer() (*Server, *http.ServeMux) {
	svc := service.New(nil, core.NewEventBus(), "", "")
	spy := &spyComposer{}
	s := &Server{
		svc:        svc,
		pipe:       pipeline.New(nil, nil),
		plugins:    &methodSpyPlugins{},
		sender:     spy,
		wsComposer: spy,
	}
	s.hub = newHub(svc)
	mux := http.NewServeMux()
	s.routes(mux)
	return s, mux
}

func serveMethod(method, path string) *httptest.ResponseRecorder {
	_, mux := wiredServer()
	req := httptest.NewRequest(method, "http://127.0.0.1:8888"+path, strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestPluginSourceMethodWhitelist 钉住 /source 的读写只认 GET/HEAD/PUT。读写共用一条路径,
// 方法一旦放宽,本想保存的请求会落到读分支,拿到 200 和旧源码,改动被静默丢弃。
func TestPluginSourceMethodWhitelist(t *testing.T) {
	const path = "/api/plugins/demo/source"
	for _, m := range allMethods {
		if contains([]string{http.MethodGet, http.MethodHead, http.MethodPut}, m) {
			continue
		}
		spy := &methodSpyPlugins{}
		rec := pluginRequest((&Server{plugins: spy}).handlePlugin, m, path, `{"source":"new"}`)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s 应返回 405,got %d", m, path, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD, PUT" {
			t.Fatalf("%s %s 的 Allow 应为 \"GET, HEAD, PUT\",got %q", m, path, got)
		}
		if spy.touched() {
			t.Fatalf("%s %s 不应产生副作用: %+v", m, path, spy)
		}
		// 被拒的请求不能顺手把源码带出去。
		if strings.Contains(rec.Body.String(), "onRequest") {
			t.Fatalf("%s %s 的 405 响应不应含插件源码: %s", m, path, rec.Body.String())
		}
	}

	spy := &methodSpyPlugins{}
	rec := pluginRequest((&Server{plugins: spy}).handlePlugin, http.MethodPut, path, `{"source":"saved"}`)
	if rec.Code != http.StatusOK || !spy.saved || spy.savedSource != "saved" {
		t.Fatalf("PUT 应保存源码: code=%d saved=%v source=%q", rec.Code, spy.saved, spy.savedSource)
	}

	spy = &methodSpyPlugins{}
	rec = pluginRequest((&Server{plugins: spy}).handlePlugin, http.MethodGet, path, "")
	if rec.Code != http.StatusOK || !spy.sourceRead {
		t.Fatalf("GET 应返回源码: code=%d read=%v", rec.Code, spy.sourceRead)
	}
}

// TestPluginActionsRejectUnexpectedMethods 逐个动作钉住方法白名单:动作由路径段决定,方法
// 一旦放宽,DELETE /api/plugins/{id}/enable 这种自相矛盾的请求会真的启用插件。
func TestPluginActionsRejectUnexpectedMethods(t *testing.T) {
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
			if contains(c.allowed, m) {
				continue
			}
			spy := &methodSpyPlugins{}
			rec := pluginRequest((&Server{plugins: spy}).handlePlugin, m, path, "{}")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s 应返回 405,got %d", m, path, rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != c.allow {
				t.Errorf("%s %s 的 Allow 应为 %q,got %q", m, path, c.allow, got)
			}
			if spy.touched() {
				t.Errorf("%s %s 不应产生副作用: %+v", m, path, spy)
			}
		}
		for _, m := range c.allowed {
			spy := &methodSpyPlugins{}
			if rec := pluginRequest((&Server{plugins: spy}).handlePlugin, m, path, "{}"); rec.Code != http.StatusOK {
				t.Errorf("%s %s 应返回 200,got %d (%s)", m, path, rec.Code, rec.Body.String())
			}
		}
	}
}

// TestPluginUnknownActionIs404 未知动作是路径问题,不是「服务器不支持该方法」,不能回 501。
func TestPluginUnknownActionIs404(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		spy := &methodSpyPlugins{}
		rec := pluginRequest((&Server{plugins: spy}).handlePlugin, m, "/api/plugins/demo/bogus", "{}")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s /api/plugins/demo/bogus 应返回 404,got %d", m, rec.Code)
		}
		if spy.touched() {
			t.Errorf("%s 未知动作不应产生副作用: %+v", m, spy)
		}
	}
}

// TestPluginExtraPathSegmentsRejected 多余路径段必须拒绝而不是忽略,否则拼错的路径会当成
// 父动作执行并回 200。
func TestPluginExtraPathSegmentsRejected(t *testing.T) {
	for _, path := range []string{
		"/api/plugins/demo/enable/extra",
		"/api/plugins/demo/logs/x/y",
		"/api/plugins/demo/source/x",
	} {
		spy := &methodSpyPlugins{}
		rec := pluginRequest((&Server{plugins: spy}).handlePlugin, http.MethodPost, path, "{}")
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s 应返回 404,got %d", path, rec.Code)
		}
		if spy.touched() {
			t.Errorf("POST %s 不应产生副作用: %+v", path, spy)
		}
	}
}

// TestPluginsCollectionMethods 集合端点只认 GET/HEAD 读、POST 建。其余方法若落到读分支,
// 用 PUT 做「创建」的调用方只看状态码会以为成功了。
func TestPluginsCollectionMethods(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		if rec := pluginRequest((&Server{plugins: &methodSpyPlugins{}}).handlePlugins, m, "/api/plugins", ""); rec.Code != http.StatusOK {
			t.Errorf("%s /api/plugins 应返回 200,got %d", m, rec.Code)
		}
	}
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, "FOO"} {
		for _, provider := range []PluginProvider{&methodSpyPlugins{}, nil} {
			rec := pluginRequest((&Server{plugins: provider}).handlePlugins, m, "/api/plugins", "{}")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s /api/plugins (provider=%T) 应返回 405,got %d", m, provider, rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD, POST" {
				t.Errorf("%s /api/plugins 的 Allow 应为 \"GET, HEAD, POST\",got %q", m, got)
			}
		}
	}
}

// TestRuleExtraPathSegmentsRejected 多余路径段一旦被忽略就会退化成对父资源动手:
// DELETE /api/intercept/rules/{id}/typo 删掉的是父规则,PUT 则把它整条覆盖。
func TestRuleExtraPathSegmentsRejected(t *testing.T) {
	for _, c := range []struct {
		method string
		suffix string
	}{
		{http.MethodDelete, "/bogus"},
		{http.MethodPut, "/bogus"},
		{http.MethodGet, "/bogus"},
		{http.MethodPost, "/toggle/extra"},
	} {
		svc := service.New(nil, nil, "", "")
		rule := svc.CreateRule(&service.InterceptRule{Name: "r1", Enabled: true})
		server := &Server{svc: svc}

		path := fmt.Sprintf("http://127.0.0.1:8888/api/intercept/rules/%s%s", rule.ID, c.suffix)
		rec := httptest.NewRecorder()
		server.handleRule(rec, httptest.NewRequest(c.method, path, strings.NewReader(`{"name":"overwritten"}`)))

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s 应返回 404,got %d", c.method, c.suffix, rec.Code)
		}
		got, found := svc.Rule(rule.ID)
		if !found {
			t.Fatalf("%s %s 删掉了父规则", c.method, c.suffix)
		}
		if got.Name != "r1" || !got.Enabled {
			t.Fatalf("%s %s 改动了父规则: name=%q enabled=%v", c.method, c.suffix, got.Name, got.Enabled)
		}
	}
}

// TestRuleToggleMethodWhitelist toggle 只认 POST/PUT;放宽到 DELETE/PATCH 会让语义相反的
// 请求也翻转开关。
func TestRuleToggleMethodWhitelist(t *testing.T) {
	for _, m := range allMethods {
		svc := service.New(nil, nil, "", "")
		rule := svc.CreateRule(&service.InterceptRule{Name: "r1", Enabled: true})
		server := &Server{svc: svc}

		path := fmt.Sprintf("http://127.0.0.1:8888/api/intercept/rules/%s/toggle", rule.ID)
		rec := httptest.NewRecorder()
		server.handleRule(rec, httptest.NewRequest(m, path, strings.NewReader(`{"enabled":false}`)))

		got, _ := svc.Rule(rule.ID)
		if m == http.MethodPost || m == http.MethodPut {
			if rec.Code != http.StatusOK || got.Enabled {
				t.Errorf("%s toggle 应生效: code=%d enabled=%v", m, rec.Code, got.Enabled)
			}
			continue
		}
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s toggle 应返回 405,got %d", m, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "POST, PUT" {
			t.Errorf("%s toggle 的 Allow 应为 \"POST, PUT\",got %q", m, allow)
		}
		if !got.Enabled {
			t.Errorf("%s toggle 被拒后不应改动开关", m)
		}
	}
}

// TestReadOnlyEndpointsRejectMutatingMethods 只读端点必须拒绝变更方法:DELETE 回 200 数据
// 会让调用方以为删成功了。
func TestReadOnlyEndpointsRejectMutatingMethods(t *testing.T) {
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
			rec := serveMethod(m, path)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s 应返回 405,got %d", m, path, rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("%s %s 的 Allow 应为 \"GET, HEAD\",got %q", m, path, allow)
			}
		}
	}
}

// TestSessionsClearOnlyPOST 清空历史只能由 POST 触发:这是全站破坏性最强的一次调用,
// 方法关口是它唯一的门槛。
func TestSessionsClearOnlyPOST(t *testing.T) {
	for _, m := range allMethods {
		rec := serveMethod(m, "/api/sessions/clear")
		if m == http.MethodPost {
			if rec.Code != http.StatusOK {
				t.Errorf("POST /api/sessions/clear 应返回 200,got %d", rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/sessions/clear 应返回 405,got %d", m, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "POST" {
			t.Errorf("%s /api/sessions/clear 的 Allow 应为 \"POST\",got %q", m, allow)
		}
	}
}

// routePaths 覆盖 routes() 注册的每一条路由,子树路由各取一个代表路径。
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
	"/api/breakpoints/abc/resume",
	"/api/export",
	"/api/ws",
}

// TestMethodNotAllowedIsSelfConsistent 全路由 × 全方法扫一遍,双向钉住 Allow:声明的方法
// 必须真能进,没声明的方法必须真被拒。手写 switch + default 的站点靠这条防止 Allow 与
// case 分支各改各的,新增端点漏写 Allow 也会在这里失败。
func TestMethodNotAllowedIsSelfConsistent(t *testing.T) {
	for _, path := range routePaths {
		var accepted, declared []string
		for _, m := range allMethods {
			rec := serveMethod(m, path)
			if rec.Code != http.StatusMethodNotAllowed {
				accepted = append(accepted, m)
				continue
			}
			allow := rec.Header().Get("Allow")
			if allow == "" {
				t.Errorf("%s %s 回了 405 却没有 Allow 头", m, path)
				continue
			}
			if declared == nil {
				declared = strings.Split(allow, ", ")
				continue
			}
			if got := strings.Join(strings.Split(allow, ", "), ", "); got != strings.Join(declared, ", ") {
				t.Errorf("%s 对不同方法给出了不一致的 Allow: %q vs %q", path, got, strings.Join(declared, ", "))
			}
		}
		if declared == nil {
			continue
		}
		sort.Strings(accepted)
		sorted := append([]string(nil), declared...)
		sort.Strings(sorted)
		if strings.Join(accepted, ",") != strings.Join(sorted, ",") {
			t.Errorf("%s 实际放行 %v,Allow 却声明 %v", path, accepted, declared)
		}
	}
}

// patternRecorder 清点 routes() 注册了哪些模式。
type patternRecorder struct{ patterns []string }

func (p *patternRecorder) HandleFunc(pattern string, _ func(http.ResponseWriter, *http.Request)) {
	p.patterns = append(p.patterns, pattern)
}

// TestRoutePathsCoverEveryRoute 把 routePaths 与 routes() 绑在一起:新增一条路由却没往
// routePaths 里补代表路径,上面两条方法矩阵就会静默漏掉它。
func TestRoutePathsCoverEveryRoute(t *testing.T) {
	rec := &patternRecorder{}
	(&Server{}).routes(rec)

	_, mux := wiredServer()
	covered := map[string]bool{}
	for _, path := range routePaths {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8888"+path, nil)
		_, pattern := mux.Handler(req)
		covered[pattern] = true
	}
	for _, pattern := range rec.patterns {
		if !covered[pattern] {
			t.Errorf("routePaths 缺少命中 %s 的代表路径", pattern)
		}
	}
}

// TestPathVariantsRejected 尾斜杠与多余路径段不得退化成对父资源动手。
func TestPathVariantsRejected(t *testing.T) {
	for _, c := range []struct {
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
	} {
		if rec := serveMethod(c.method, c.path); rec.Code != c.want {
			t.Errorf("%s %s 应返回 %d,got %d", c.method, c.path, c.want, rec.Code)
		}
	}
}

// TestUnavailableSubsystemKeepsMethodContract 未装配的子系统对读方法回空清单、对变更方法回
// 501「本次构建没有它」;它与方法约束是两件正交的事,收紧方法不该把这条回退分支带偏。
func TestUnavailableSubsystemKeepsMethodContract(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		if rec := pluginRequest((&Server{}).handlePlugins, m, "/api/plugins", ""); rec.Code != http.StatusOK {
			t.Errorf("%s /api/plugins (无插件子系统) 应返回 200,got %d", m, rec.Code)
		}
		for _, h := range []http.HandlerFunc{(&Server{}).handleBreakpoints, (&Server{}).handleBreakpointRules} {
			if rec := pluginRequest(h, m, "/api/breakpoints", ""); rec.Code != http.StatusOK {
				t.Errorf("%s 断点列表 (无 pipeline) 应返回 200,got %d", m, rec.Code)
			}
		}
	}
	if rec := pluginRequest((&Server{}).handlePlugins, http.MethodPost, "/api/plugins", "{}"); rec.Code != http.StatusNotImplemented {
		t.Errorf("POST /api/plugins (无插件子系统) 应返回 501,got %d", rec.Code)
	}
	for _, h := range []http.HandlerFunc{(&Server{}).handleBreakpoints, (&Server{}).handleBreakpointRules} {
		if rec := pluginRequest(h, http.MethodPost, "/api/breakpoints", "{}"); rec.Code != http.StatusNotImplemented {
			t.Errorf("POST 断点 (无 pipeline) 应返回 501,got %d", rec.Code)
		}
	}
}

// TestHeadMatchesGet 凡 GET 读得到的路径,HEAD 必须同码 —— 客户端拿 HEAD 探活是常规做法,
// 两者分叉会让探活结果与真实可读性对不上。
func TestHeadMatchesGet(t *testing.T) {
	for _, path := range routePaths {
		if path == "/api/ws" {
			// WebSocket 握手只认 GET,HEAD 无意义。
			continue
		}
		get := serveMethod(http.MethodGet, path).Code
		if get == http.StatusMethodNotAllowed {
			continue
		}
		if head := serveMethod(http.MethodHead, path).Code; head != get {
			t.Errorf("%s: HEAD 回 %d,GET 回 %d", path, head, get)
		}
	}
}
