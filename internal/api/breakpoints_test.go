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

// 本文件对应 breakpoints.go 全部处理器:暂停队列的处置、全局开关、URL 规则 CRUD。
// 子系统未装配(pipe 为 nil)的回退矩阵统一在 method_test.go。

// breakpointRule 解出一条 URL 断点规则。
func breakpointRule(t *testing.T, rec *httptest.ResponseRecorder) *pipeline.BreakRule {
	t.Helper()
	var rule pipeline.BreakRule
	decodeEnvelope(t, rec).into(t, &rule)
	return &rule
}

// TestBreakpointResumeVsAbortOutcome 把 resume 分支换成 Abort(或把 resume-all 换成 AbortAll),
// 只看 200 / resolved 计数 / 列表清空的断言全绿 —— 这三样在阻断路径上完全一致。
// 回归后用户在断点面板点「放行」,请求被静默掐断,页面加载失败且无任何提示。
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

// TestBreakpointResumeAppliesEdit API 层此前只覆盖「非法编辑被拒」与「空体放行 200」,
// 没有一条证明合法编辑真的落到了 flow 上。JSON tag 漂移、decode 目标改成非指针、patch 合并被跳过,
// 都会让端点回 200 而用户改的 URL/头/体被静默丢弃 —— 断点编辑器的全部价值就在这一步。
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

// TestBreakpointResumeRejectsUnknownFields strictFields 是全包唯一的 DisallowUnknownFields 调用点,
// 去掉它只是删一个实参、不会有任何测试变红。放宽后把整个 flow 原样 POST 回来的客户端拿到 200,
// 而 request.body 在 flow 那边是 base64 字节、在 patch 这边是明文,上游真正收到的是一串 base64 字面量。
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

// TestBreakpointResumeRejectsOversizedBody 断点侧靠 errBodyTooLarge 哨兵在 decodeBreakpointJSON 与
// failBreakpointDecode 之间协调「响应已写过」。这道协调被删后状态码仍是先写入的 413,只看状态码的
// 断言照样通过,body 却变成 {413 信封}{400 信封} —— 客户端 JSON.parse 整个 body 直接抛错,
// 超限被显示成「响应解析失败」。
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

// TestBreakpointResumeSeparatesInvalidFromMissing 编辑不合法是 400 且 flow 继续被按住;
// flow 已不在暂停中才是 404。两者混成一个码,客户端就分不清「改回来还能重来」和「这一条已经走了」。
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
	// 文案原文透传是断点编辑器唯一能告诉用户「哪一行头非法」的通道;
	// 换成通用的 "invalid edit" 后,用户面对几十行的头部编辑器只会看到「请求无效」。
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

// TestBreakpointWriteEndpointsRejectInvalidBodies failBreakpointDecode 的 400 分支此前执行计数为 0:
// errors.Is 判断被改错时处理器 return 时一个字节都没写,net/http 会补一个空体 200,
// 客户端据此认为全局断点已设置成功。而这些入口的字段全是「零值有意义」的 bool,
// 空体若被当成合法全零对象,一次误发的无体 POST 就会把用户开着的开关全部关掉并回 200。
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

	// 对照组:只有 resume 允许空体(不带编辑的放行是最常见的一次点击)。
	t.Run("resume 允许空体", func(t *testing.T) {
		id, _, wait := pausedFlow(t, bp)
		if rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200", rec.Code)
		}
		wait()
	})
}

// TestBreakpointRuleCreateDefaults enabled 缺省为 true 是这里唯一的业务判断。退成 false 后用户在界面
// 新建的 URL 断点规则一条都不生效 —— 请求照常放过去,列表里那条规则看着还在,极难自查。
// 空白 url 被放过则生成一条匹配空串的规则,等于对全部流量开断点。
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

		// 回执里的 id 必须真能在列表里找到,否则前端后续的 toggle/delete 全 404。
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

// TestBreakpointRuleUpdateFieldSemantics 同一请求里 Enabled 走 patch(缺省保留)、
// OnRequest/OnResponse 走整体覆盖(缺省即关闭),是最容易在重构时被「统一」掉的地方:
// 统一成 patch,用户再也关不掉某个阶段的断点;统一成覆盖,PUT 一次 URL 就顺手把用户禁用的规则
// 重新启用,流量突然全部断住。
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

	t.Run("阶段开关整体覆盖而 enabled 缺省保留", func(t *testing.T) {
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
			t.Error("enabled 缺省应保留原值(原值为 false)")
		}
		if got := bp.ListRules()[0]; got.URL != updated.URL || got.OnRequest != updated.OnRequest || got.Enabled != updated.Enabled {
			t.Errorf("回执与 store 不一致: %+v vs %+v", updated, got)
		}
	})
}

// TestBreakpointRuleGetDeleteAndNotFound breakpointRuleByID 的命中分支此前从未执行过。写错比较字段
// (例如按 URL 比)会让规则详情页永远 404,或让 DELETE 误删同名 URL 的另一条规则;
// 而重复 DELETE 回 404 是前端判断「这条已被别的窗口删了」的唯一信号。
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

// TestBreakpointRuleToggleRejections body.Enabled 是 *bool 且强制必填,正是为了让 `{}` 不被当成「禁用」。
// 简化成非指针 bool 后,前端任何一次载荷字段名写错都会静默关掉用户的断点规则,界面上开关自己弹回去;
// 未知第二段若落进下面的分支,DELETE /rules/{id}/enable 会真的删掉规则。
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

// TestBreakpointGlobalSwitchRoundTrip 同一条 /api/breakpoints 路径上 GET 读暂停清单、POST 写全局开关,
// 读写不同义,是最容易被「对称化」改坏的一处;而 /global 的 GET 是 UI 重开断点面板时唯一的状态来源。
// 读回链路一断,用户看到开关是关的、请求却继续被断住(或反过来),两种都无法靠肉眼归因。
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

	// 旧入口写的是同一份状态:兼容路径与新路径分家就会让两个界面各说各话。
	if rec := do(t, mux, http.MethodPost, "/api/breakpoints", `{"onRequest":false,"onResponse":true}`); rec.Code != http.StatusOK {
		t.Fatalf("旧入口 POST 状态码 = %d", rec.Code)
	}
	if got := readGlobal(); got.OnRequest || !got.OnResponse {
		t.Errorf("旧入口写入后读回 %+v,期望 false/true", got)
	}

	// GET /api/breakpoints 读的是暂停清单,不是开关状态。
	listRec := do(t, mux, http.MethodGet, "/api/breakpoints", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("暂停清单状态码 = %d", listRec.Code)
	}
	if got := string(decodeEnvelope(t, listRec).Data); got != "[]" {
		t.Errorf("未挂断点时暂停清单 = %s,期望 []", got)
	}
}

// TestBreakpointAbortAllBlocksQueue resume-all 有测试而 abort-all 没有,两者只差一个函数引用,
// 重构时极易被指向同一实现。若 abort-all 实际走了 ResumeAll,用户点「全部阻断」后几十条被断住的
// 请求会照常发给上游 —— 这是断点面板上唯一带破坏性语义的按钮,失效方向是「该拦的没拦」。
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

	// 空队列上再来一次:计数为 0 而不是报错,前端连点两下不该弹错误。
	rec = do(t, mux, http.MethodPost, "/api/breakpoints/abort-all", "")
	decodeEnvelope(t, rec).into(t, &body)
	if rec.Code != http.StatusOK || body["resolved"] != 0 {
		t.Errorf("空队列上的批量阻断 = %d / resolved %d", rec.Code, body["resolved"])
	}
}

// TestBreakpointResumeAllClearsQueue 全局断点一开,一个页面几十个并发请求会同时断住,
// 逐条处置不是可用的操作;批量放行必须真的是「放行」而不是阻断。
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

// TestBreakpointExtendContract 续期存在的意义就是防止编辑中的请求被超时静默放行。未命中若回 200 +
// 零值时间,UI 会把倒计时刷成 0001-01-01 或当成续期成功,用户继续编辑一份早已被自动放行的请求。
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

// TestBreakpointPathSegmentsRejected 安全基线要求多余段不得退化成对父资源动手,
// sessions / plugins / intercept 都已逐条守住,唯独断点这两个手写切分的处理器一条没列。
// 它们的失效方式最直接:一个多打了一段的 URL 若落进 resume 分支,会把用户正在编辑的请求提前发出去。
func TestBreakpointPathSegmentsRejected(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	id, _, wait := pausedFlow(t, bp)
	rule := bp.AddRuleWithEnabled("a.com", true, false, true)

	// 断点侧现行是 400 而不是基线里的 404,如实记录这处分叉。
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
