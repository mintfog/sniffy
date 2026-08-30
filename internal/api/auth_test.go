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

// 本文件只测 authMiddleware 一件事:谁能进、谁被挡在管理 API 之外。方法白名单、路径解析
// 一类的关口归 method_test.go —— 混在这里会让「鉴权到底测到什么程度」无从清点。

// authProbe 是 authMiddleware 加一个只记录「是否被放行」的内层处理器。
// 断言必须同时看状态码与放行标志:只看状态码分不清「被中间件拒了」和「透传后处理器自己回了 403」。
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

// assert 核对一次探针请求的结果。被拒时连文案一起核对:管理 API 不发 CORS 头,
// 那段文案是调用方唯一拿得到的纠正线索。
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

// TestAuthLoopbackHostMatrix 无 token 的兜底路径靠 Host 头判定回环,这是 DNS rebinding 的唯一闸口:
// 把精确比较改成前后缀匹配,任意网页即可读走全部抓包内容并改配置。
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
		// 以下三条是已知分叉,不是笔误:Host 侧按精确小写比较(Origin 侧却用 EqualFold),
		// 带方括号的 IPv6 缺了端口时 SplitHostPort 失败、ParseIP 又认不得方括号,
		// 而 "localhost." 这个合法的 FQDN 形式不在名单里。三者都是 fail-closed,
		// 浏览器也发不出这种 Host,故如实记录而不迁就。
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

// TestAuthSameOriginHeaderMatrix Origin 与 Sec-Fetch-Site 是挡住浏览器发起的跨站请求的第二道关口。
// Origin: null 来自 sandbox iframe 与 file:// 页面,放宽这里等于把变更权限交给任意页面。
func TestAuthSameOriginHeaderMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		origin string
		site   string
		allow  bool
	}{
		{"无 Origin 无 Sec-Fetch-Site", "", "", true}, // 命令行脚本的常规形态
		{"Sec-Fetch-Site: none", "", "none", true},  // 地址栏直接打开
		{"Sec-Fetch-Site: same-origin", "", "same-origin", true},
		{"Origin 大小写不敏感", "HTTP://127.0.0.1:8888", "", true},
		{"Origin 带路径仍只比 host", "http://127.0.0.1:8888/some/path", "", true},

		{"Origin: null", "null", "", false},
		{"Origin: file://", "file://", "", false},
		{"跨站 Origin", "http://evil.example", "", false},
		{"同机不同端口 Origin", "http://127.0.0.1:9999", "", false},
		{"跨站 Sec-Fetch-Site", "", "cross-site", false},
		{"同站不同源 Sec-Fetch-Site", "", "same-site", false},
		// Sec-Fetch-Site 的取值由浏览器生成,大小写与空白都是固定的,按精确匹配 fail-closed。
		{"大写变体不被认可", "", "Same-Origin", false},
		{"尾随空格不被认可", "", "same-origin ", false},
		{"多值不被认可", "", "same-origin, cross-site", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newAuthProbe(&Server{})
			var opts []reqOpt
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

// TestAuthBearerHeaderBoundaries token 比较是定长常量时间比较。改成 HasPrefix/Contains 就能
// 逐字节爆破 token;而末尾的 TrimSpace 是反向契约:token 来自 api_token 文件,
// 用户 cat 粘贴几乎必然带上换行,去掉它所有人都会「token 明明对却 401」。
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

// TestAuthTokenModeSkipsSameOriginCheck token 模式刻意不看 Host/Origin —— 远程 HTTPS 管理 API
// (-api-tls-cert 配 -allow-insecure-api)的 Host 本就不是回环。有人「多加一层防护」把同源检查
// 并进 token 分支,所有非回环部署会每个请求回 403,而 Host 恒为 127.0.0.1 的用例一条都不会红。
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

// TestAuthQueryTokenOnlyOnExactWSPath query 里的 token 会进 access log、浏览器历史与 Referer,
// 所以只有加不上 Authorization 头的 WebSocket 握手能用它,且必须是精确路径。
// 放宽成前缀匹配后,泄漏过的一条 URL 就等于交出所有前缀匹配到的端点。
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

	// 头部一旦出现就只认头部:否则一个错的头配上一个对的 query 也能进,
	// 「凭证该放哪儿」就不再有唯一答案。
	t.Run("错误的头不被 query token 兜底", func(t *testing.T) {
		t.Parallel()
		p := newAuthProbe(&Server{token: "secret"})
		rec := do(t, p, http.MethodGet, "/api/ws", "",
			withQuery("token=secret"), withHeader("Authorization", "Bearer wrong"))
		p.assert(t, rec, false, http.StatusUnauthorized, "unauthorized")
	})
}

// TestAuthRejectionLeaksNothing 中间件包在整个 mux 外层,未认证者既枚举不到路由也拿不到方法白名单。
// auth 一旦下沉成逐路由包装,攻击者靠 401/404/405 的差异就能画出整张管理 API 地图 ——
// 而这张图上有 /api/config(明文上游代理密码)与 /api/certificate/export(CA 私钥)。
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
