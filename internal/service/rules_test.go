// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// ruleIDs 把规则列表压成 ID 序列,用于比对顺序。
func ruleIDs(rules []*InterceptRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.ID)
	}
	return out
}

// newRule 造一条最简规则(条件/动作各一)。
func newRule(name string, enabled bool) *InterceptRule {
	return &InterceptRule{
		Name:    name,
		Enabled: enabled,
		Conditions: []InterceptCondition{
			{Type: "url", Operator: "contains", Value: "example.com"},
		},
		Actions: []InterceptAction{
			{Type: "block", Parameters: map[string]any{"status": float64(403)}, Enabled: true},
		},
	}
}

func TestRuleStoreCreateAssignsIdentity(t *testing.T) {
	t.Parallel()
	rs := newRuleStore("")

	r := rs.create(newRule("拦截广告", true))
	if !strings.HasPrefix(r.ID, "rule-") {
		t.Errorf("ID 应带 rule- 前缀,实际 %q", r.ID)
	}
	if _, err := time.Parse(time.RFC3339, r.CreatedAt); err != nil {
		t.Errorf("CreatedAt 应为 RFC3339: %q (%v)", r.CreatedAt, err)
	}
	if r.UpdatedAt != r.CreatedAt {
		t.Errorf("新建时 UpdatedAt 应等于 CreatedAt: %q vs %q", r.UpdatedAt, r.CreatedAt)
	}
	// 前端不填逻辑运算符时按 AND 处理。
	if r.LogicOperator != "AND" {
		t.Errorf("缺省逻辑运算符 = %q, want AND", r.LogicOperator)
	}

	or := newRule("任一命中", true)
	or.LogicOperator = "OR"
	if got := rs.create(or); got.LogicOperator != "OR" {
		t.Errorf("显式逻辑运算符应保留,实际 %q", got.LogicOperator)
	}

	// 每条规则各自独立的 ID。
	if rs.list()[0].ID == rs.list()[1].ID {
		t.Error("两条规则不应共用 ID")
	}
}

// TestRuleStoreListKeepsInsertionOrder 规则按创建顺序展示(Priority 才是执行顺序),不受 map 遍历影响。
func TestRuleStoreListKeepsInsertionOrder(t *testing.T) {
	t.Parallel()
	rs := newRuleStore("")
	var want []string
	for i := range 5 {
		want = append(want, rs.create(newRule(fmt.Sprintf("r%d", i), true)).ID)
	}
	if got := ruleIDs(rs.list()); !slices.Equal(got, want) {
		t.Fatalf("列表顺序 = %v, want %v", got, want)
	}
}

func TestRuleStoreGet(t *testing.T) {
	t.Parallel()
	rs := newRuleStore("")
	created := rs.create(newRule("规则", true))

	got, ok := rs.get(created.ID)
	if !ok || got.Name != "规则" {
		t.Fatalf("按 ID 应查到规则: %+v ok=%v", got, ok)
	}
	if _, ok := rs.get("rule-missing"); ok {
		t.Error("未知 ID 不应查到规则")
	}
}

// TestRuleStoreUpdatePreservesIdentity 更新是整体替换,但 ID 与创建时间属于服务端,
// 不能被前端提交的 body 顶掉。
func TestRuleStoreUpdatePreservesIdentity(t *testing.T) {
	t.Parallel()
	rs := newRuleStore("")
	created := rs.create(newRule("原名", true))

	incoming := newRule("新名", false)
	incoming.ID = "rule-伪造"
	incoming.CreatedAt = "1999-01-01T00:00:00Z"
	updated, ok := rs.update(created.ID, incoming)
	if !ok {
		t.Fatal("更新已存在的规则应成功")
	}
	if updated.ID != created.ID {
		t.Errorf("ID 应保持 %q,实际 %q", created.ID, updated.ID)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Errorf("CreatedAt 应保持 %q,实际 %q", created.CreatedAt, updated.CreatedAt)
	}
	if _, err := time.Parse(time.RFC3339, updated.UpdatedAt); err != nil {
		t.Errorf("UpdatedAt 应刷新为 RFC3339: %q (%v)", updated.UpdatedAt, err)
	}
	if stored, _ := rs.get(created.ID); stored.Name != "新名" || stored.Enabled {
		t.Errorf("内容应被替换: %+v", stored)
	}
	if got := ruleIDs(rs.list()); !slices.Equal(got, []string{created.ID}) {
		t.Errorf("更新不应改变列表顺序: %v", got)
	}

	if _, ok := rs.update("rule-missing", newRule("x", true)); ok {
		t.Error("更新不存在的规则应返回 false")
	}
}

func TestRuleStoreToggle(t *testing.T) {
	t.Parallel()
	rs := newRuleStore("")
	created := rs.create(newRule("规则", true))

	off, ok := rs.toggle(created.ID, false)
	if !ok || off.Enabled {
		t.Fatalf("关闭失败: %+v ok=%v", off, ok)
	}
	on, ok := rs.toggle(created.ID, true)
	if !ok || !on.Enabled {
		t.Fatalf("开启失败: %+v ok=%v", on, ok)
	}
	if _, err := time.Parse(time.RFC3339, on.UpdatedAt); err != nil {
		t.Errorf("切换应刷新 UpdatedAt: %q (%v)", on.UpdatedAt, err)
	}
	if _, ok := rs.toggle("rule-missing", true); ok {
		t.Error("切换不存在的规则应返回 false")
	}
}

func TestRuleStoreDeleteKeepsOrder(t *testing.T) {
	t.Parallel()
	rs := newRuleStore("")
	a := rs.create(newRule("a", true))
	b := rs.create(newRule("b", true))
	c := rs.create(newRule("c", true))

	rs.delete("rule-missing") // 不存在的 ID 应为 no-op
	rs.delete(b.ID)

	if got := ruleIDs(rs.list()); !slices.Equal(got, []string{a.ID, c.ID}) {
		t.Fatalf("删除中间元素后顺序 = %v, want [%s %s]", got, a.ID, c.ID)
	}
	if _, ok := rs.get(b.ID); ok {
		t.Error("删除后不应还能查到")
	}
}

func TestRuleStoreStats(t *testing.T) {
	t.Parallel()
	rs := newRuleStore("")
	if got := rs.stats(); got.TotalRules != 0 || got.ActiveRules != 0 {
		t.Fatalf("空存储统计 = %+v", got)
	}
	rs.create(newRule("on-1", true))
	rs.create(newRule("on-2", true))
	off := rs.create(newRule("off", false))

	got := rs.stats()
	if got.TotalRules != 3 || got.ActiveRules != 2 {
		t.Fatalf("统计 = %+v, want 3/2", got)
	}
	rs.delete(off.ID)
	if got := rs.stats(); got.TotalRules != 2 || got.ActiveRules != 2 {
		t.Fatalf("删除后统计 = %+v, want 2/2", got)
	}
}

// TestRuleStorePersistenceRoundTrip 规则落盘后重建应原样回来,含顺序与开关。
func TestRuleStorePersistenceRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rules.json")
	rs := newRuleStore(path)
	first := rs.create(newRule("第一条", true))
	second := rs.create(newRule("第二条", false))
	rs.toggle(first.ID, false)

	reloaded := newRuleStore(path)
	got := reloaded.list()
	if ids := ruleIDs(got); !slices.Equal(ids, []string{first.ID, second.ID}) {
		t.Fatalf("重载顺序 = %v, want [%s %s]", ids, first.ID, second.ID)
	}
	if got[0].Enabled || got[0].Name != "第一条" {
		t.Errorf("重载后第一条 = %+v", got[0])
	}
	if len(got[0].Conditions) != 1 || got[0].Conditions[0].Value != "example.com" {
		t.Errorf("条件应原样回来: %+v", got[0].Conditions)
	}
	if len(got[0].Actions) != 1 || got[0].Actions[0].Parameters["status"] != float64(403) {
		t.Errorf("动作参数应原样回来: %+v", got[0].Actions)
	}

	// 删除同样要落盘,否则重启后规则复活。
	reloaded.delete(second.ID)
	if got := newRuleStore(path).list(); len(got) != 1 {
		t.Fatalf("删除应落盘,重载后剩 %d 条", len(got))
	}
}

// TestRuleStoreSurvivesBrokenFile 规则文件被外部编辑坏掉时按空规则集起步,不阻塞启动。
func TestRuleStoreSurvivesBrokenFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
	}{
		{"非法 JSON", "{ 这不是 JSON"},
		{"类型不匹配", `{"rules": []}`},
		{"空文件", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "rules.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			rs := newRuleStore(path)
			if got := rs.list(); len(got) != 0 {
				t.Fatalf("坏文件应按空规则集起步,实际 %d 条", len(got))
			}
			// 起步后仍可正常写入并覆盖坏文件。
			created := rs.create(newRule("恢复", true))
			if got := newRuleStore(path).list(); len(got) != 1 || got[0].ID != created.ID {
				t.Fatalf("坏文件应被新内容覆盖: %+v", got)
			}
		})
	}
}

// TestRuleStoreConcurrent 规则由 UI 改、由核心钩子读,-race 下压出并发问题。
func TestRuleStoreConcurrent(t *testing.T) {
	t.Parallel()
	rs := newRuleStore("")

	const workers = 8
	var wg sync.WaitGroup
	ids := make(chan string, workers*10)
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range 10 {
				ids <- rs.create(newRule(fmt.Sprintf("w%d-%d", w, i), true)).ID
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range workers * 10 {
			rs.list()
			rs.stats()
		}
	}()
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, workers*10)
	for id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("并发创建产生了重复 ID: %s", id)
		}
		seen[id] = struct{}{}
	}
	if got := rs.stats(); got.TotalRules != workers*10 {
		t.Fatalf("规则数 = %d, want %d", got.TotalRules, workers*10)
	}
}

// TestServiceRuleAPIDelegates 守住 Service 到 ruleStore 的方法转发(两种 transport 都只经 Service)。
func TestServiceRuleAPIDelegates(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	created := svc.CreateRule(newRule("规则", true))
	if created.ID == "" {
		t.Fatal("创建应回填 ID")
	}
	if got, ok := svc.Rule(created.ID); !ok || got.Name != "规则" {
		t.Fatalf("Rule = %+v ok=%v", got, ok)
	}
	if got := svc.Rules(); len(got) != 1 {
		t.Fatalf("Rules = %d 条, want 1", len(got))
	}
	if got, ok := svc.UpdateRule(created.ID, newRule("改名", true)); !ok || got.Name != "改名" {
		t.Fatalf("UpdateRule = %+v ok=%v", got, ok)
	}
	if got, ok := svc.ToggleRule(created.ID, false); !ok || got.Enabled {
		t.Fatalf("ToggleRule = %+v ok=%v", got, ok)
	}
	if got := svc.RuleStats(); got.TotalRules != 1 || got.ActiveRules != 0 {
		t.Fatalf("RuleStats = %+v, want 1/0", got)
	}
	svc.DeleteRule(created.ID)
	if got := svc.Rules(); len(got) != 0 {
		t.Fatalf("删除后 Rules = %d 条, want 0", len(got))
	}
}
