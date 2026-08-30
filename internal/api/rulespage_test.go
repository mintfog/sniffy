// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

// 本文件只放规则端点自己算的分页切片。分页字段的完整边界表在 response_test.go 的
// TestPaginatedEnvelopeBoundaries —— handleRules 有一套自己的 start/end 钳制,两处都要守。

// TestRulesPageOverflowReturnsEmptyPage (page-1)*pageSize 溢出为负时按越界页处理,
// 且同一次响应里 paginated 的乘法溢出也不得让空页反报「还有下一页」。
func TestRulesPageOverflowReturnsEmptyPage(t *testing.T) {
	t.Parallel()
	// 2^62+1:与 pageSize=2 相乘恰好回绕成 int64 最小值。
	const overflowPage = "4611686018427387905"

	s, mux := newTestServer(t)
	s.svc.CreateRule(&service.InterceptRule{Name: "r1"})

	rec := do(t, mux, http.MethodGet, "/api/intercept/rules?page="+overflowPage+"&pageSize=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	page := decodePage(t, rec)

	var rules []*service.InterceptRule
	if err := json.Unmarshal(page.Data, &rules); err != nil {
		t.Fatalf("解析规则页失败: %v (%s)", err, page.Data)
	}
	if len(rules) != 0 {
		t.Errorf("越界页应为空,实际 %d 条", len(rules))
	}
	if page.Total != 1 {
		t.Errorf("total = %d,期望 1", page.Total)
	}
	if page.HasNext {
		t.Error("空的越界页不应报「还有下一页」,否则按 hasNext 递增翻页的客户端永远停不下来")
	}
	if !page.HasPrev {
		t.Error("越界页的 hasPrev 应为 true")
	}
}

// TestRulesLastPageReturnsTailItem 末页只剩一条,且必须是最后插入的那条。
// 只断言「1 条 + total 3」的话,偏移算错(少减一个 pageSize、或顺序倒过来)时第 2 页返回 r1
// 照样通过,用户翻页反复看到同一批规则而唯一守着分页正确性的测试是绿的。
func TestRulesLastPageReturnsTailItem(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	for _, name := range []string{"r1", "r2", "r3"} {
		s.svc.CreateRule(&service.InterceptRule{Name: name})
	}

	page := decodePage(t, do(t, mux, http.MethodGet, "/api/intercept/rules?page=2&pageSize=2", ""))
	var rules []*service.InterceptRule
	if err := json.Unmarshal(page.Data, &rules); err != nil {
		t.Fatalf("解析规则页失败: %v (%s)", err, page.Data)
	}
	if page.Total != 3 || len(rules) != 1 {
		t.Fatalf("第 2 页应为 1 条 / total 3,实际 %d 条 / total %d", len(rules), page.Total)
	}
	if rules[0].Name != "r3" {
		t.Errorf("末页那条 = %q,期望 \"r3\"", rules[0].Name)
	}
}
