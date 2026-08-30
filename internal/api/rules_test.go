// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/service"
)

// 本文件对应 rules.go 的增改删与集合路径;分页留在 rulespage_test.go。
// 此前的覆盖全部集中在「被拒的方法/路径」,正常的增改删一次都没跑过。

// seedRules 预置若干规则,返回它们的 ID(顺序与传入名字一致)。
func seedRules(t *testing.T, s *Server, names ...string) []string {
	t.Helper()
	ids := make([]string, 0, len(names))
	for _, name := range names {
		ids = append(ids, s.svc.CreateRule(&service.InterceptRule{Name: name, Enabled: true}).ID)
	}
	return ids
}

// TestRuleUpdateReplacesWholeRule PUT 是整体覆盖而不是合并。语义若反过来,前端保存规则时会
// 静默清空/保留用户没提交的字段(禁用的规则被复活、条件被清空);请求体 id 若能改写路径 id,
// store 里会出现 key 与 ID 不一致的规则,之后 toggle/delete 全部 404。
func TestRuleUpdateReplacesWholeRule(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	original := s.svc.CreateRule(&service.InterceptRule{
		Name:       "r1",
		Enabled:    true,
		Priority:   7,
		Conditions: []service.InterceptCondition{{Type: "host", Operator: "equals", Value: "a.com"}},
	})

	rec := do(t, mux, http.MethodPut, "/api/intercept/rules/"+original.ID,
		`{"name":"r1-new","id":"rule-forged"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var updated service.InterceptRule
	decodeEnvelope(t, rec).into(t, &updated)

	if updated.ID != original.ID {
		t.Errorf("data.id = %q,期望沿用路径 id %q(请求体里的 id 必须被忽略)", updated.ID, original.ID)
	}
	if updated.CreatedAt != original.CreatedAt {
		t.Errorf("createdAt = %q,期望保持 %q", updated.CreatedAt, original.CreatedAt)
	}
	if updated.Name != "r1-new" {
		t.Errorf("name = %q,期望 \"r1-new\"", updated.Name)
	}
	// 整体覆盖:请求体没提的字段一律回到零值。
	if updated.Enabled || updated.Priority != 0 || len(updated.Conditions) != 0 {
		t.Errorf("未提交的字段应被覆盖成零值,got enabled=%v priority=%d conditions=%d",
			updated.Enabled, updated.Priority, len(updated.Conditions))
	}
	// updatedAt 只到秒,同一秒内更新前后可能相等 —— 只断言它仍是合法时间戳。
	if _, err := time.Parse(time.RFC3339, updated.UpdatedAt); err != nil {
		t.Errorf("updatedAt %q 不是 RFC3339: %v", updated.UpdatedAt, err)
	}

	stored, found := s.svc.Rule(original.ID)
	if !found || stored.Name != "r1-new" || stored.Enabled {
		t.Errorf("store 里的规则 = %+v", stored)
	}
	if _, found := s.svc.Rule("rule-forged"); found {
		t.Error("请求体里的 id 不应在 store 里另起一条")
	}
}

// TestRuleUpdateRejections 未命中与畸形 JSON 都不得改动任何东西。
func TestRuleUpdateRejections(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	ids := seedRules(t, s, "r1")

	t.Run("未知 id 回 404", func(t *testing.T) {
		rec := do(t, mux, http.MethodPut, "/api/intercept/rules/ghost", `{"name":"x"}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "rule not found" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("畸形 JSON 回 400 且不改动", func(t *testing.T) {
		rec := do(t, mux, http.MethodPut, "/api/intercept/rules/"+ids[0], `{`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "invalid json" {
			t.Errorf("message = %q", e.Message)
		}
	})

	got, found := s.svc.Rule(ids[0])
	if !found || got.Name != "r1" || !got.Enabled {
		t.Errorf("被拒的请求改动了规则: %+v", got)
	}
	if n := len(s.svc.Rules()); n != 1 {
		t.Errorf("规则总数 = %d,期望 1", n)
	}
}

// TestRuleCollectionPathRejectsMutations 空 id 是唯一一条「看起来像集合端点」的输入。
// 这道守卫退化成集合语义时,一次 DELETE /api/intercept/rules/ 就会对用户整份拦截规则动手,
// 而调用方只看到 200。
func TestRuleCollectionPathRejectsMutations(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	seedRules(t, s, "r1", "r2", "r3")

	for _, c := range []struct{ method, body string }{
		{http.MethodDelete, ""},
		{http.MethodPut, `{"name":"x"}`},
	} {
		t.Run(c.method, func(t *testing.T) {
			rec := do(t, mux, c.method, "/api/intercept/rules/", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d,期望 400", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "invalid rule id" {
				t.Errorf("message = %q,期望 \"invalid rule id\"", e.Message)
			}
		})
	}

	rules := s.svc.Rules()
	if len(rules) != 3 {
		t.Fatalf("规则总数 = %d,期望 3", len(rules))
	}
	for i, want := range []string{"r1", "r2", "r3"} {
		if rules[i].Name != want || !rules[i].Enabled {
			t.Errorf("第 %d 条被改动了: %+v", i, rules[i])
		}
	}
}

// TestRuleCreateReturnsPersistedRule 前端拿创建接口的返回体直接插进列表并以其 id 做后续 toggle/delete。
// 返回体丢了 id 或 logicOperator 默认值,用户新建的规则在界面上存在、后续操作却全 404,
// 或条件按空逻辑符匹配导致规则默默不生效。
func TestRuleCreateReturnsPersistedRule(t *testing.T) {
	t.Parallel()

	t.Run("创建成功", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/intercept/rules", `{"name":"r1","enabled":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		var created service.InterceptRule
		decodeEnvelope(t, rec).into(t, &created)

		if !strings.HasPrefix(created.ID, "rule-") {
			t.Errorf("id = %q,期望 \"rule-\" 前缀", created.ID)
		}
		if created.CreatedAt == "" || created.UpdatedAt == "" {
			t.Errorf("时间戳 = %q/%q", created.CreatedAt, created.UpdatedAt)
		}
		if created.LogicOperator != "AND" {
			t.Errorf("logicOperator = %q,期望默认值 \"AND\"", created.LogicOperator)
		}
		if created.Name != "r1" || !created.Enabled {
			t.Errorf("name/enabled = %q/%v", created.Name, created.Enabled)
		}

		// 返回的 id 必须真能在列表里找到,否则前端后续所有操作都会 404。
		page := decodePage(t, do(t, mux, http.MethodGet, "/api/intercept/rules", ""))
		if page.Total != 1 {
			t.Fatalf("列表 total = %d,期望 1", page.Total)
		}
		if _, found := s.svc.Rule(created.ID); !found {
			t.Errorf("返回的 id %q 在 store 里找不到", created.ID)
		}
	})

	t.Run("畸形 JSON 不建规则", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/intercept/rules", `not json`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "invalid json" {
			t.Errorf("message = %q", e.Message)
		}
		if n := len(s.svc.Rules()); n != 0 {
			t.Errorf("不应建出规则,实际 %d 条", n)
		}
	})
}

// TestRuleGetAndDelete 删除是本端点破坏性最强的一条,此前正常删除一次都没跑过:
// 删错条目、少删、或把幂等改成 404(前端重复点删除弹「规则不存在」)都不会被发现。
func TestRuleGetAndDelete(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	ids := seedRules(t, s, "r1", "r2", "r3")

	t.Run("取详情命中", func(t *testing.T) {
		rec := do(t, mux, http.MethodGet, "/api/intercept/rules/"+ids[1], "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		var got service.InterceptRule
		decodeEnvelope(t, rec).into(t, &got)
		if got.ID != ids[1] || got.Name != "r2" {
			t.Errorf("详情 = %q/%q,期望 %q/\"r2\"", got.ID, got.Name, ids[1])
		}
	})

	t.Run("取详情未命中", func(t *testing.T) {
		rec := do(t, mux, http.MethodGet, "/api/intercept/rules/ghost", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "rule not found" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("删除只动目标条目", func(t *testing.T) {
		rec := do(t, mux, http.MethodDelete, "/api/intercept/rules/"+ids[1], "")
		if rec.Code != http.StatusOK || !decodeEnvelope(t, rec).Success {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		if _, found := s.svc.Rule(ids[1]); found {
			t.Error("目标规则仍在")
		}
		rules := s.svc.Rules()
		if len(rules) != 2 || rules[0].Name != "r1" || rules[1].Name != "r3" {
			t.Errorf("剩余规则 = %+v,期望 [r1 r3](顺序保持)", rules)
		}
	})

	// 幂等:另一个窗口先删掉时,重复删除不该报错。
	t.Run("重复删除与未知 id 都回 200", func(t *testing.T) {
		for _, id := range []string{ids[1], "ghost"} {
			if rec := do(t, mux, http.MethodDelete, "/api/intercept/rules/"+id, ""); rec.Code != http.StatusOK {
				t.Errorf("DELETE %s = %d,期望 200(幂等)", id, rec.Code)
			}
		}
		if n := len(s.svc.Rules()); n != 2 {
			t.Errorf("规则总数 = %d,期望仍为 2", n)
		}
	})
}

// TestRuleToggleIgnoresBodyDecodeErrors toggle 刻意忽略解码错误,空体因此等价于「关闭」。
// 任何截断 / Content-Type 写错 / 被中间件吃掉 body 的 toggle 请求,都会把用户本想启用的规则
// 静默关掉,UI 只看到 200 —— 用户以为规则开着,抓包却完全不生效。
func TestRuleToggleIgnoresBodyDecodeErrors(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, body string }{
		{"不带 body", ""},
		{"截断的 JSON", `{`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			ids := seedRules(t, s, "r1")

			rec := do(t, mux, http.MethodPost, "/api/intercept/rules/"+ids[0]+"/toggle", c.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
			}
			var got service.InterceptRule
			decodeEnvelope(t, rec).into(t, &got)
			if got.Enabled {
				t.Error("回执里的 enabled 应为 false(空体解成零值)")
			}
			// 回执与实际状态不得分叉:分叉时界面开关会与抓包行为长期不一致。
			if stored, _ := s.svc.Rule(ids[0]); stored.Enabled {
				t.Error("store 里的 enabled 应为 false")
			}
		})
	}
}
