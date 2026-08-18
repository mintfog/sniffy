// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

// TestRulesPageOverflowReturnsEmptyPage (page-1)*pageSize 溢出为负时按越界页处理。
func TestRulesPageOverflowReturnsEmptyPage(t *testing.T) {
	t.Parallel()
	// 2^62+1:与 pageSize=2 相乘恰好回绕成 int64 最小值。
	const overflowPage = "4611686018427387905"

	svc := service.New(nil, nil, "", "")
	svc.CreateRule(&service.InterceptRule{Name: "r1"})
	server := &Server{svc: svc}

	rec := httptest.NewRecorder()
	server.handleRules(rec, httptest.NewRequest(http.MethodGet, "/api/intercept/rules?page="+overflowPage+"&pageSize=2", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200,实际 %d,响应: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data  []*service.InterceptRule `json:"data"`
		Total int                      `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Data) != 0 {
		t.Fatalf("越界页应为空,实际 %d 条", len(resp.Data))
	}
	if resp.Total != 1 {
		t.Fatalf("total 应为 1,实际 %d", resp.Total)
	}
}

// TestRulesFirstPageUnchanged 常规分页取中间页。
func TestRulesFirstPageUnchanged(t *testing.T) {
	t.Parallel()
	svc := service.New(nil, nil, "", "")
	svc.CreateRule(&service.InterceptRule{Name: "r1"})
	svc.CreateRule(&service.InterceptRule{Name: "r2"})
	svc.CreateRule(&service.InterceptRule{Name: "r3"})
	server := &Server{svc: svc}

	rec := httptest.NewRecorder()
	server.handleRules(rec, httptest.NewRequest(http.MethodGet, "/api/intercept/rules?page=2&pageSize=2", nil))

	var resp struct {
		Data  []*service.InterceptRule `json:"data"`
		Total int                      `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Data) != 1 || resp.Total != 3 {
		t.Fatalf("第 2 页应为 1 条 / total 3,实际 %d 条 / total %d", len(resp.Data), resp.Total)
	}
}
