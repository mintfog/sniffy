// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package rules

import (
	"context"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

// 规则引擎以 RegisterCore 挂进管道,必须同时满足请求侧与响应侧钩子接口,
// 否则 bootstrap 里那次注册会静默降级(类型断言失败即不入表),规则全部失效。
var (
	_ pipeline.RequestHook  = (*Engine)(nil)
	_ pipeline.ResponseHook = (*Engine)(nil)
)

func TestHookIdentity(t *testing.T) {
	e := engineWith()
	if got := e.Name(); got != "rules-engine" {
		t.Errorf("Name() = %q", got)
	}
	// 管道把核心钩子与插件钩子混排后按 Priority 升序执行,插件 manifest 默认 100;
	// 0 保证规则先于插件改写流量,调到大于 100 会反过来。
	if got := e.Priority(); got != 0 {
		t.Errorf("Priority() = %d want 0", got)
	}
	// 启停与 URL 门控由每条规则自己的 Enabled/Conditions 决定,引擎本身必须恒为通过,
	// 否则 pipeline 会在调用 OnRequest 之前就把它跳过。
	if !e.Enabled() {
		t.Error("Enabled() should always be true")
	}
	for _, u := range []string{"", "https://x.com/a", "not a url"} {
		if !e.Match(u) {
			t.Errorf("Match(%q) should be true", u)
		}
	}
}

// TestEngineThroughPipeline 走真实管道:只有 Enabled()/Match() 都放行,
// 核心钩子才会被调用,规则动作才会落到流量上。
func TestEngineThroughPipeline(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Name:          "deny-analytics",
		Conditions:    []service.InterceptCondition{cond("url_host", "contains", "analytics")},
		Actions:       []service.InterceptAction{{Type: "block", Parameters: map[string]any{}}},
	}
	p := pipeline.New(nil, nil)
	p.RegisterCore(engineWith(r))

	f := reqFlow("GET", "https://analytics.example.com/t", "analytics.example.com", "/t")
	d := p.OnRequest(context.Background(), f)
	if d.Kind != flow.Abort {
		t.Fatalf("expected Abort through pipeline, got %v", d.Kind)
	}
	if d.StatusOnAbort != 403 {
		t.Errorf("status = %d want 403", d.StatusOnAbort)
	}

	// 响应侧同理:改状态码的规则必须经管道生效。
	r.Conditions = nil
	r.Actions = []service.InterceptAction{{Type: "modify_status", Parameters: map[string]any{"statusCode": float64(500)}}}
	f2 := reqFlow("GET", "https://x.com/", "x.com", "/")
	f2.Response = &flow.Response{Status: 200, Header: map[string][]string{}}
	if d := p.OnResponse(context.Background(), f2); d.Kind != flow.Continue {
		t.Fatalf("expected Continue, got %v", d.Kind)
	}
	if f2.Response.Status != 500 {
		t.Errorf("status = %d want 500", f2.Response.Status)
	}
}

// TestReplaceBodyCreatesHeaderMap 覆盖 Request.Header 为 nil 的流量:
// 抓包侧存在无头映射的构造路径,直接写 map 会 panic。
func TestReplaceBodyCreatesHeaderMap(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Actions: []service.InterceptAction{{Type: "replace_body", Parameters: map[string]any{
			"body": "x", "contentType": "text/plain",
		}}},
	}
	f := reqFlow("POST", "https://x.com/api", "x.com", "/api")
	f.Request.Header = nil
	if d := engineWith(r).OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("expected Continue, got %v", d.Kind)
	}
	if got := f.Request.Header["Content-Type"]; len(got) != 1 || got[0] != "text/plain" {
		t.Fatalf("content-type = %v", got)
	}
	if string(f.Request.Body) != "x" {
		t.Errorf("body = %s", f.Request.Body)
	}
}

func TestGetStr(t *testing.T) {
	if getStr(nil, "k") != "" {
		t.Error("nil map should return empty")
	}
	m := map[string]any{"s": "v", "n": float64(3), "b": true, "bad": []int{1}}
	cases := map[string]string{"s": "v", "n": "3", "b": "true", "bad": "", "missing": ""}
	for key, want := range cases {
		if got := getStr(m, key); got != want {
			t.Errorf("getStr(%q) = %q want %q", key, got, want)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("= %q want a", got)
	}
	if got := firstNonEmpty("", "b"); got != "b" {
		t.Errorf("= %q want b", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("= %q want empty", got)
	}
}
