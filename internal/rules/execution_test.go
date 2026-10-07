// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package rules

import (
	"context"
	"reflect"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

func TestRuleExecutionPhases(t *testing.T) {
	rules := []*service.InterceptRule{
		{ID: "disabled", Enabled: false, Actions: []service.InterceptAction{{Type: "block"}}},
		{ID: "unmatched", Enabled: true, Conditions: []service.InterceptCondition{cond("url_host", "equals", "other.test")}, Actions: []service.InterceptAction{{Type: "block"}}},
		{ID: "empty", Enabled: true},
		{ID: "unknown", Enabled: true, Actions: []service.InterceptAction{{Type: "unknown"}}},
		{ID: "both", Name: "两个阶段", Enabled: true, Actions: []service.InterceptAction{
			{Type: "modify_method", Parameters: map[string]any{"newMethod": "POST"}},
			{Type: "replace_body", Parameters: map[string]any{"body": "内容"}},
			{Type: "modify_status", Parameters: map[string]any{"statusCode": 201}},
		}},
		{ID: "response", Name: "响应规则", Enabled: true, Conditions: []service.InterceptCondition{cond("response_status", "equals", "201")}, Actions: []service.InterceptAction{
			{Type: "modify_response_headers", Parameters: map[string]any{"name": "X-Test", "value": "yes"}},
		}},
	}
	p := pipeline.New(nil, nil)
	p.RegisterCore(engineWith(rules...))
	f := reqFlow("GET", "https://example.test/", "example.test", "/")
	ctx := context.Background()
	p.OnRequest(ctx, f)
	want := []flow.Execution{{Kind: "rule", ID: "both", Name: "两个阶段", Phase: flow.PhaseRequest}}
	if !reflect.DeepEqual(f.Executions, want) {
		t.Fatalf("请求阶段执行记录 = %+v, want %+v", f.Executions, want)
	}
	f.Response = &flow.Response{Status: 200, Header: map[string][]string{}}
	p.OnResponse(ctx, f)
	want = append(want,
		flow.Execution{Kind: "rule", ID: "both", Name: "两个阶段", Phase: flow.PhaseResponse},
		flow.Execution{Kind: "rule", ID: "response", Name: "响应规则", Phase: flow.PhaseResponse},
	)
	rules[4].Name = "新名称"
	if !reflect.DeepEqual(f.Executions, want) {
		t.Fatalf("完整执行记录 = %+v, want %+v", f.Executions, want)
	}
}

func TestRuleExecutionShortCircuit(t *testing.T) {
	for _, action := range []string{"block", "auto_respond"} {
		t.Run(action, func(t *testing.T) {
			e := engineWith(
				&service.InterceptRule{ID: "first", Enabled: true, Actions: []service.InterceptAction{
					{Type: "modify_method", Parameters: map[string]any{"newMethod": "POST"}},
					{Type: action},
				}},
				&service.InterceptRule{ID: "later", Enabled: true, Actions: []service.InterceptAction{{Type: "block"}}},
			)
			f := reqFlow("GET", "https://example.test/", "example.test", "/")
			e.OnRequest(context.Background(), f)
			if len(f.Executions) != 1 || f.Executions[0].ID != "first" {
				t.Fatalf("短路执行记录 = %+v, want 仅 first", f.Executions)
			}
		})
	}
}

func BenchmarkRuleExecutions(b *testing.B) {
	for _, host := range []string{"example.test", "other.test"} {
		b.Run(host, func(b *testing.B) {
			e := engineWith(&service.InterceptRule{
				ID: "rule", Name: "测试规则", Enabled: true,
				Conditions: []service.InterceptCondition{cond("url_host", "equals", "example.test")},
				Actions:    []service.InterceptAction{{Type: "modify_method", Parameters: map[string]any{"newMethod": "POST"}}},
			})
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				req := flow.Request{URL: "https://" + host + "/", Host: host, Method: "GET"}
				f := flow.Flow{Request: &req}
				e.OnRequest(ctx, &f)
			}
		})
	}
}
