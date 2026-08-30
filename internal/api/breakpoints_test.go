// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/pipeline"
)

// 本文件覆盖 breakpoints.go 的暂停队列、全局开关和 URL 规则 CRUD；子系统回退矩阵见 method_test.go。

// breakpointRule 解出一条 URL 断点规则。
func breakpointRule(t *testing.T, rec *httptest.ResponseRecorder) *pipeline.BreakRule {
	t.Helper()
	var rule pipeline.BreakRule
	decodeEnvelope(t, rec).into(t, &rule)
	return &rule
}

// TestBreakpointResumeVsAbortOutcome 放行与阻断共享 200 响应、resolved 计数和清空后的列表，
// Pause 返回值是区分两种处置结果的契约。
func TestBreakpointResumeVsAbortOutcome(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		action    string
		wantAbort bool
	}{
		{"resume", false},
		{"abort", true},
	} {
		t.Run(c.action, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			id, _, wait := pausedFlow(t, s.pipe.Breakpoints())

			rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/"+c.action, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
			}
			if got := wait(); got != c.wantAbort {
				t.Errorf("Pause 返回 abort=%v,期望 %v", got, c.wantAbort)
			}
			if n := len(s.pipe.Breakpoints().List()); n != 0 {
				t.Errorf("处置后队列仍有 %d 条", n)
			}
		})
	}
}

// TestBreakpointResumeAppliesEdit 合法编辑必须写回 flow 并标记 Modified；空体放行保留原请求字段。
func TestBreakpointResumeAppliesEdit(t *testing.T) {
	t.Parallel()

	t.Run("编辑落到 flow 上", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		id, f, wait := pausedFlow(t, s.pipe.Breakpoints())

		rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume",
			`{"request":{"url":"https://y.com/edited","method":"PUT"}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		wait()
		if f.Request.URL != "https://y.com/edited" || f.Request.Method != http.MethodPut {
			t.Errorf("编辑未落到 flow 上: %s %s", f.Request.Method, f.Request.URL)
		}
		if !f.Modified {
			t.Error("改过的 flow 应被标记为 Modified(UI 靠它区分被改过的请求)")
		}
	})

	t.Run("空体放行不动任何字段", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		id, f, wait := pausedFlow(t, s.pipe.Breakpoints())
		before := *f.Request

		if rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		wait()
		if f.Request.URL != before.URL || f.Request.Method != before.Method {
			t.Errorf("空体放行改动了请求行: %s %s", f.Request.Method, f.Request.URL)
		}
		if f.Modified {
			t.Error("没改过的 flow 不应被标记为 Modified")
		}
	})
}

// TestBreakpointResumeRejectsUnknownFields resume 载荷严格拒绝未知字段，避免把 flow 快照误当成编辑内容；
// request.body 在 flow 中是 base64、在 patch 中是明文，字段契约必须保持清晰。
func TestBreakpointResumeRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, body string }{
		{"顶层未知键", `{"id":"x","request":{"url":"https://y.com/"}}`},
		{"嵌套未知键", `{"request":{"url":"https://y.com/","bodyEncoding":"base64"}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			bp := s.pipe.Breakpoints()
			id, _, wait := pausedFlow(t, bp)

			rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d,期望 400", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Success || e.Message != "invalid json" {
				t.Errorf("响应 = success:%v message:%q", e.Success, e.Message)
			}
			if n := len(bp.List()); n != 1 {
				t.Errorf("被拒的放行不应把 flow 从断点上放走,剩余 %d 条", n)
			}
			_ = bp.Abort(id)
			wait()
		})
	}
}

// TestBreakpointResumeRejectsOversizedBody 超限响应由 errBodyTooLarge 协调 decodeBreakpointJSON 与
// failBreakpointDecode，只写出一个 413 信封，客户端可直接解析错误信息。
func TestBreakpointResumeRejectsOversizedBody(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	id, _, wait := pausedFlow(t, bp)

	huge := `{"request":{"body":"` + strings.Repeat("a", maxBreakpointBody+1024) + `"}}`
	rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", huge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d,期望 413", rec.Code)
	}
	if n := len(bp.List()); n != 1 {
		t.Error("被拒绝的放行不应把 flow 从断点上放走")
	}

	// 整个响应体必须恰好是一个 JSON 值。
	dec := json.NewDecoder(rec.Body)
	var first apiResponse
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("响应体不是 apiResponse: %v", err)
	}
	if first.Success || first.Message != "request body is too large" {
		t.Errorf("响应 = success:%v message:%q", first.Success, first.Message)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		t.Errorf("响应体后面还跟着东西(413 之后又写了一份):%v", err)
	}

	_ = bp.Abort(id)
	wait()
}

// TestBreakpointResumeSeparatesInvalidFromMissing 非法编辑返回 400 并保留暂停状态；已解除的 flow 返回 404。
func TestBreakpointResumeSeparatesInvalidFromMissing(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	id, _, wait := pausedFlow(t, bp)

	bad := `{"request":{"headers":[["X-Evil","a\r\nX-Injected: 1"]]}}`
	rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法编辑状态码 = %d,期望 400", rec.Code)
	}
	// 错误文案保留具体字段名，断点编辑器才能定位非法的请求头。
	if msg := decodeEnvelope(t, rec).Message; !strings.Contains(msg, "X-Evil") {
		t.Errorf("message = %q,期望点名出问题的头 X-Evil", msg)
	}
	if n := len(bp.List()); n != 1 {
		t.Fatal("校验失败不应放行 flow")
	}

	if rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
		t.Fatalf("改回来后放行 = %d", rec.Code)
	}
	wait()

	rec = do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("已解除的断点 = %d,期望 404", rec.Code)
	}
	if e := decodeEnvelope(t, rec); e.Message != "breakpoint not found" {
		t.Errorf("message = %q", e.Message)
	}
}

// TestBreakpointWriteEndpointsRejectInvalidBodies 全局开关、规则创建和 toggle 的 JSON 载荷必须完整有效；
// 这些入口的布尔零值有业务含义，空体与截断 JSON 统一返回 400 并保留原状态。
func TestBreakpointWriteEndpointsRejectInvalidBodies(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	bp.SetGlobalBreak(true, true)
	rule := bp.AddRuleWithEnabled("api.x.com", true, true, true)

	endpoints := []struct{ name, method, path string }{
		{"旧的全局入口", http.MethodPost, "/api/breakpoints"},
		{"全局 PUT", http.MethodPut, "/api/breakpoints/global"},
		{"全局 POST", http.MethodPost, "/api/breakpoints/global"},
		{"新增规则", http.MethodPost, "/api/breakpoints/rules"},
		{"单条规则 PUT", http.MethodPut, "/api/breakpoints/rules/" + rule.ID},
		{"规则 toggle", http.MethodPost, "/api/breakpoints/rules/" + rule.ID + "/toggle"},
	}
	for _, ep := range endpoints {
		for _, body := range []struct{ name, value string }{
			{"截断的 JSON", `{`},
			{"空体", ""},
		} {
			t.Run(ep.name+"/"+body.name, func(t *testing.T) {
				rec := do(t, mux, ep.method, ep.path, body.value)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("状态码 = %d,期望 400", rec.Code)
				}
				if e := decodeEnvelope(t, rec); e.Success || e.Message != "invalid json" {
					t.Errorf("响应 = success:%v message:%q", e.Success, e.Message)
				}
			})
		}
	}

	if onReq, onResp := bp.GlobalBreak(); !onReq || !onResp {
		t.Errorf("被拒的请求关掉了全局开关: onRequest=%v onResponse=%v", onReq, onResp)
	}
	rules := bp.ListRules()
	if len(rules) != 1 || !rules[0].Enabled {
		t.Errorf("被拒的请求动了 URL 规则: %+v", rules)
	}

	// resume 的空体表示不带编辑的放行，是唯一允许的空体写操作。
	t.Run("resume 允许空体", func(t *testing.T) {
		id, _, wait := pausedFlow(t, bp)
		if rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200", rec.Code)
		}
		wait()
	})
}

// TestBreakpointRuleCreateDefaults 新建规则默认启用且必须提供非空 URL；空白 URL 不能生成匹配全部流量的规则。
func TestBreakpointRuleCreateDefaults(t *testing.T) {
	t.Parallel()

	t.Run("缺 url 一律拒绝", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		for _, body := range []string{`{"url":"   "}`, `{}`} {
			rec := do(t, mux, http.MethodPost, "/api/breakpoints/rules", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("body=%s 状态码 = %d,期望 400", body, rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "url is required" {
				t.Errorf("body=%s message = %q", body, e.Message)
			}
		}
		if n := len(s.pipe.Breakpoints().ListRules()); n != 0 {
			t.Errorf("不应建出规则,实际 %d 条", n)
		}
	})

	t.Run("enabled 缺省为 true", func(t *testing.T) {
		t.Parallel()
		_, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/rules", `{"url":"api.x.com","onRequest":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		created := breakpointRule(t, rec)
		if !created.Enabled {
			t.Error("新建的规则应默认启用,否则用户建完发现断点不生效")
		}
		if !created.OnRequest || created.OnResponse {
			t.Errorf("阶段开关 = %v/%v,期望 true/false", created.OnRequest, created.OnResponse)
		}
		if !strings.HasPrefix(created.ID, "bp-") {
			t.Errorf("id = %q,期望 \"bp-\" 前缀", created.ID)
		}

		// 回执里的 id 必须能在列表中定位，供前端继续 toggle/delete。
		listRec := do(t, mux, http.MethodGet, "/api/breakpoints/rules", "")
		var list []*pipeline.BreakRule
		decodeEnvelope(t, listRec).into(t, &list)
		if len(list) != 1 || list[0].ID != created.ID || list[0].URL != "api.x.com" {
			t.Errorf("列表 = %+v,期望恰有回执里那一条", list)
		}
	})

	t.Run("显式 enabled:false 被尊重", func(t *testing.T) {
		t.Parallel()
		_, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/rules", `{"url":"a","enabled":false}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		if breakpointRule(t, rec).Enabled {
			t.Error("显式传 false 时不应被缺省值覆盖")
		}
	})
}

// TestBreakpointRuleUpdateFieldSemantics Enabled 使用 *bool patch（缺省保留原值），
// OnRequest/OnResponse 使用整体覆盖（缺省为 false）；启用和禁用两种原值都要验证。
func TestBreakpointRuleUpdateFieldSemantics(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	rule := bp.AddRuleWithEnabled("a.com", true, false, false)

	t.Run("缺 url 一律拒绝且不改动", func(t *testing.T) {
		for _, body := range []string{`{"url":""}`, `{}`} {
			rec := do(t, mux, http.MethodPut, "/api/breakpoints/rules/"+rule.ID, body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("body=%s 状态码 = %d,期望 400", body, rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "url is required" {
				t.Errorf("body=%s message = %q", body, e.Message)
			}
		}
		got := bp.ListRules()[0]
		if got.URL != "a.com" || !got.OnRequest || got.Enabled {
			t.Errorf("被拒的请求改动了规则: %+v", got)
		}
	})

	t.Run("未知 id 回 404", func(t *testing.T) {
		rec := do(t, mux, http.MethodPut, "/api/breakpoints/rules/ghost", `{"url":"a.com"}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "breakpoint rule not found" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("阶段开关整体覆盖,禁用中的规则不被缺省重新启用", func(t *testing.T) {
		rec := do(t, mux, http.MethodPut, "/api/breakpoints/rules/"+rule.ID, `{"url":"b.com"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		updated := breakpointRule(t, rec)
		if updated.URL != "b.com" {
			t.Errorf("url = %q,期望 \"b.com\"", updated.URL)
		}
		if updated.OnRequest {
			t.Error("onRequest 缺省即覆盖成 false")
		}
		if updated.Enabled {
			t.Error("缺省不得把禁用中的规则重新启用")
		}
		if got := bp.ListRules()[0]; got.URL != updated.URL || got.OnRequest != updated.OnRequest || got.Enabled != updated.Enabled {
			t.Errorf("回执与 store 不一致: %+v vs %+v", updated, got)
		}
	})

	t.Run("启用中的规则不被缺省静默关掉", func(t *testing.T) {
		enabled := bp.AddRuleWithEnabled("c.com", false, true, true)

		rec := do(t, mux, http.MethodPut, "/api/breakpoints/rules/"+enabled.ID, `{"url":"d.com"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		updated := breakpointRule(t, rec)
		if !updated.Enabled {
			t.Error("缺省不得把启用中的规则静默关掉 —— 用户改一次 URL 断点就再也不命中了")
		}
		if updated.OnResponse {
			t.Error("onResponse 缺省仍应被覆盖成 false")
		}

		// 显式给值时以显式值为准。
		rec = do(t, mux, http.MethodPut, "/api/breakpoints/rules/"+enabled.ID, `{"url":"d.com","enabled":false}`)
		if got := breakpointRule(t, rec); got.Enabled {
			t.Error("显式 enabled:false 应被尊重")
		}
	})
}

// TestBreakpointRuleGetDeleteAndNotFound 详情和删除按规则 ID 定位；重复删除返回 404，供多窗口同步状态。
func TestBreakpointRuleGetDeleteAndNotFound(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	r1 := bp.AddRuleWithEnabled("a.com", true, false, true)
	r2 := bp.AddRuleWithEnabled("b.com", false, true, true)

	t.Run("取详情命中", func(t *testing.T) {
		rec := do(t, mux, http.MethodGet, "/api/breakpoints/rules/"+r1.ID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		if got := breakpointRule(t, rec); got.ID != r1.ID || got.URL != "a.com" {
			t.Errorf("详情 = %+v,期望 %q/a.com", got, r1.ID)
		}
	})

	t.Run("取详情未命中", func(t *testing.T) {
		rec := do(t, mux, http.MethodGet, "/api/breakpoints/rules/nope", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "breakpoint rule not found" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("删除只动目标条目", func(t *testing.T) {
		rec := do(t, mux, http.MethodDelete, "/api/breakpoints/rules/"+r1.ID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		rules := bp.ListRules()
		if len(rules) != 1 || rules[0].ID != r2.ID {
			t.Errorf("剩余规则 = %+v,期望只剩 %q", rules, r2.ID)
		}
	})

	t.Run("重复删除回 404", func(t *testing.T) {
		rec := do(t, mux, http.MethodDelete, "/api/breakpoints/rules/"+r1.ID, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
	})
}

// TestBreakpointRuleToggleRejections toggle 的 enabled 字段必须显式提供；未知动作与未知 ID 均保持规则原状。
func TestBreakpointRuleToggleRejections(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	rule := bp.AddRuleWithEnabled("a.com", true, false, true)

	t.Run("缺 enabled 字段回 400", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/rules/"+rule.ID+"/toggle", `{}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "enabled is required" {
			t.Errorf("message = %q", e.Message)
		}
		if !bp.ListRules()[0].Enabled {
			t.Error("被拒的 toggle 不应关掉规则")
		}
	})

	t.Run("未知 id 回 404", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/rules/ghost/toggle", `{"enabled":false}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "breakpoint rule not found" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("未知动作回 404 且不动规则", func(t *testing.T) {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			rec := do(t, mux, method, "/api/breakpoints/rules/"+rule.ID+"/enable", `{"enabled":false}`)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s 状态码 = %d,期望 404", method, rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "unknown action" {
				t.Errorf("%s message = %q", method, e.Message)
			}
		}
		rules := bp.ListRules()
		if len(rules) != 1 || !rules[0].Enabled {
			t.Errorf("未知动作动了规则: %+v", rules)
		}
	})

	t.Run("正向 toggle 生效", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/rules/"+rule.ID+"/toggle", `{"enabled":false}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		if breakpointRule(t, rec).Enabled {
			t.Error("回执里的 enabled 应为 false")
		}
		if bp.ListRules()[0].Enabled {
			t.Error("store 里的 enabled 应为 false")
		}
	})
}

// TestBreakpointGlobalSwitchRoundTrip /api/breakpoints 读暂停清单，/api/breakpoints/global 读写全局开关；
// 两条路径共享同一份状态，面板重开时据此恢复开关。
func TestBreakpointGlobalSwitchRoundTrip(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()

	readGlobal := func() breakpointGlobalState {
		t.Helper()
		rec := do(t, mux, http.MethodGet, "/api/breakpoints/global", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("读全局开关状态码 = %d", rec.Code)
		}
		var got breakpointGlobalState
		decodeEnvelope(t, rec).into(t, &got)
		return got
	}

	rec := do(t, mux, http.MethodPut, "/api/breakpoints/global", `{"onRequest":true,"onResponse":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT 状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var echoed breakpointGlobalState
	decodeEnvelope(t, rec).into(t, &echoed)
	if !echoed.OnRequest || echoed.OnResponse {
		t.Errorf("PUT 回执 = %+v,期望 true/false", echoed)
	}
	if got := readGlobal(); got != echoed {
		t.Errorf("GET 读回 %+v,与 PUT 回执 %+v 不一致", got, echoed)
	}
	if onReq, onResp := bp.GlobalBreak(); !onReq || onResp {
		t.Errorf("pipeline 侧 = %v/%v,期望 true/false", onReq, onResp)
	}

	// 旧入口与新入口写入同一份全局状态。
	if rec := do(t, mux, http.MethodPost, "/api/breakpoints", `{"onRequest":false,"onResponse":true}`); rec.Code != http.StatusOK {
		t.Fatalf("旧入口 POST 状态码 = %d", rec.Code)
	}
	if got := readGlobal(); got.OnRequest || !got.OnResponse {
		t.Errorf("旧入口写入后读回 %+v,期望 false/true", got)
	}

	// GET /api/breakpoints 返回暂停清单。
	listRec := do(t, mux, http.MethodGet, "/api/breakpoints", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("暂停清单状态码 = %d", listRec.Code)
	}
	if got := string(decodeEnvelope(t, listRec).Data); got != "[]" {
		t.Errorf("未挂断点时暂停清单 = %s,期望 []", got)
	}
}

// TestBreakpointAbortAllBlocksQueue 批量阻断让队列中的每条 Pause 返回阻断结果并清空队列；空队列操作幂等。
func TestBreakpointAbortAllBlocksQueue(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	waits := make([]func() bool, 0, 3)
	for range 3 {
		_, _, wait := pausedFlow(t, bp)
		waits = append(waits, wait)
	}

	rec := do(t, mux, http.MethodPost, "/api/breakpoints/abort-all", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var body map[string]int
	decodeEnvelope(t, rec).into(t, &body)
	if body["resolved"] != 3 {
		t.Errorf("处置条数 = %d,期望 3", body["resolved"])
	}
	for i, wait := range waits {
		if !wait() {
			t.Errorf("第 %d 条应被阻断(Pause 返回 true),实际被放行", i)
		}
	}
	if n := len(bp.List()); n != 0 {
		t.Errorf("批量阻断后仍剩 %d 条", n)
	}

	// 空队列返回 resolved=0，前端重复点击仍可安全结束操作。
	rec = do(t, mux, http.MethodPost, "/api/breakpoints/abort-all", "")
	decodeEnvelope(t, rec).into(t, &body)
	if rec.Code != http.StatusOK || body["resolved"] != 0 {
		t.Errorf("空队列上的批量阻断 = %d / resolved %d", rec.Code, body["resolved"])
	}
}

// TestBreakpointResumeAllClearsQueue 批量放行让所有暂停请求继续执行并清空队列，适配页面并发请求。
func TestBreakpointResumeAllClearsQueue(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	waits := make([]func() bool, 0, 3)
	for range 3 {
		_, _, wait := pausedFlow(t, bp)
		waits = append(waits, wait)
	}

	rec := do(t, mux, http.MethodPost, "/api/breakpoints/resume-all", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var body map[string]int
	decodeEnvelope(t, rec).into(t, &body)
	if body["resolved"] != 3 {
		t.Errorf("处置条数 = %d,期望 3", body["resolved"])
	}
	for i, wait := range waits {
		if wait() {
			t.Errorf("第 %d 条应被放行(Pause 返回 false),实际被阻断", i)
		}
	}
	if n := len(bp.List()); n != 0 {
		t.Errorf("批量放行后仍剩 %d 条", n)
	}
}

// TestBreakpointExtendContract 续期返回新的截止时间并同步更新列表；未知或已解除的断点返回 404 且不带截止时间。
func TestBreakpointExtendContract(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	id, _, wait := pausedFlow(t, bp)

	before := bp.List()[0].PausedUntil
	rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/extend", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var deadline breakpointDeadline
	decodeEnvelope(t, rec).into(t, &deadline)
	if !deadline.PausedUntil.After(before) {
		t.Errorf("续期后的截止时刻 %v 不晚于原值 %v", deadline.PausedUntil, before)
	}
	if got := bp.List()[0].PausedUntil; !got.Equal(deadline.PausedUntil) {
		t.Errorf("列表里的截止时刻 %v 与返回值 %v 不一致", got, deadline.PausedUntil)
	}

	t.Run("未知 id 回 404 且不带 pausedUntil", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/ghost/extend", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Success || e.Message != "breakpoint not found" {
			t.Errorf("响应 = success:%v message:%q", e.Success, e.Message)
		}
		if strings.Contains(rec.Body.String(), "pausedUntil") {
			t.Errorf("404 不应带零值时间戳: %s", rec.Body.String())
		}
	})

	_ = bp.Abort(id)
	wait()

	t.Run("已解除的断点回 404", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/extend", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
	})
}

// TestBreakpointPathSegmentsRejected 多余路径段按当前端点契约返回 400/404，暂停队列与规则均保持原状。
func TestBreakpointPathSegmentsRejected(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	id, _, wait := pausedFlow(t, bp)
	rule := bp.AddRuleWithEnabled("a.com", true, false, true)

	// 断点 ID 解析错误返回 400，未知动作返回 404。
	cases := []struct {
		path    string
		want    int
		wantMsg string
	}{
		{"/api/breakpoints/" + id + "/resume/junk", http.StatusBadRequest, "invalid breakpoint id"},
		{"/api/breakpoints/" + id, http.StatusBadRequest, "invalid breakpoint id"},
		{"/api/breakpoints/", http.StatusBadRequest, "invalid breakpoint id"},
		{"/api/breakpoints/" + id + "/bogus", http.StatusNotFound, "unknown action"},
		{"/api/breakpoints/rules/" + rule.ID + "/toggle/junk", http.StatusBadRequest, "invalid breakpoint rule id"},
		{"/api/breakpoints/resume-all/junk", http.StatusNotFound, "unknown action"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			rec := do(t, mux, http.MethodPost, c.path, "")
			if rec.Code != c.want {
				t.Errorf("状态码 = %d,期望 %d", rec.Code, c.want)
			}
			if e := decodeEnvelope(t, rec); e.Message != c.wantMsg {
				t.Errorf("message = %q,期望 %q", e.Message, c.wantMsg)
			}
		})
	}

	if n := len(bp.List()); n != 1 {
		t.Errorf("多余段的请求放走了被按住的 flow,剩余 %d 条", n)
	}
	if rules := bp.ListRules(); len(rules) != 1 || !rules[0].Enabled {
		t.Errorf("多余段的请求动了 URL 规则: %+v", rules)
	}

	_ = bp.Abort(id)
	wait()
}
