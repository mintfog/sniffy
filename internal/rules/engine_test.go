// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package rules

import (
	"context"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

func reqFlow(method, url, host, path string) *flow.Flow {
	f := flow.New(flow.ProtoHTTPS)
	f.Request = &flow.Request{
		Method: method,
		URL:    url,
		Host:   host,
		Path:   path,
		Header: map[string][]string{},
		Body:   []byte(`{"env":"prod"}`),
	}
	return f
}

func engineWith(rs ...*service.InterceptRule) *Engine {
	return New(func() []*service.InterceptRule { return rs })
}

func cond(t, op string, v any) service.InterceptCondition {
	return service.InterceptCondition{Type: t, Operator: op, Value: v}
}

func TestBlockAbortsOnHostMatch(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Conditions:    []service.InterceptCondition{cond("url_host", "contains", "analytics")},
		Actions:       []service.InterceptAction{{Type: "block", Parameters: map[string]any{}}},
	}
	e := engineWith(r)
	f := reqFlow("GET", "https://analytics.example.com/t", "analytics.example.com", "/t")
	d := e.OnRequest(context.Background(), f)
	if d.Kind != flow.Abort {
		t.Fatalf("expected Abort, got %v", d.Kind)
	}
	// 不匹配的 host 不应阻断。
	f2 := reqFlow("GET", "https://api.example.com/t", "api.example.com", "/t")
	if d2 := e.OnRequest(context.Background(), f2); d2.Kind != flow.Continue {
		t.Fatalf("expected Continue for non-match, got %v", d2.Kind)
	}
}

func TestRedirectSwapsHostKeepsPath(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Conditions:    []service.InterceptCondition{cond("url_host", "equals", "api.example.com")},
		Actions:       []service.InterceptAction{{Type: "redirect", Parameters: map[string]any{"url": "http://127.0.0.1:3000"}}},
	}
	e := engineWith(r)
	f := reqFlow("GET", "https://api.example.com/v1/users?id=1", "api.example.com", "/v1/users")
	if d := e.OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("expected Continue, got %v", d.Kind)
	}
	if f.Request.Host != "127.0.0.1:3000" {
		t.Fatalf("host not swapped: %s", f.Request.Host)
	}
	if f.Request.URL != "http://127.0.0.1:3000/v1/users?id=1" {
		t.Fatalf("redirect url wrong: %s", f.Request.URL)
	}
	if !f.Modified {
		t.Fatal("flow should be marked modified")
	}
}

func TestRedirectSchemelessTarget(t *testing.T) {
	mk := func(target string) *flow.Flow {
		r := &service.InterceptRule{
			Enabled:       true,
			LogicOperator: "AND",
			Conditions:    []service.InterceptCondition{cond("url_host", "equals", "api.example.com")},
			Actions:       []service.InterceptAction{{Type: "redirect", Parameters: map[string]any{"url": target}}},
		}
		f := reqFlow("GET", "https://api.example.com/v1/users?id=1", "api.example.com", "/v1/users")
		engineWith(r).OnRequest(context.Background(), f)
		return f
	}
	// 无 scheme 的 host:port —— 之前会产出 "localhost://..." 垃圾 URL。
	if f := mk("localhost:3000"); f.Request.Host != "localhost:3000" || f.Request.URL != "https://localhost:3000/v1/users?id=1" {
		t.Fatalf("host:port redirect wrong: host=%s url=%s", f.Request.Host, f.Request.URL)
	}
	// IP:port —— 之前会让 url.Parse 直接失败。
	if f := mk("127.0.0.1:3000"); f.Request.Host != "127.0.0.1:3000" {
		t.Fatalf("ip:port redirect wrong: host=%s url=%s", f.Request.Host, f.Request.URL)
	}
	// 裸 host —— 之前会被当成 path。
	if f := mk("example.com"); f.Request.Host != "example.com" {
		t.Fatalf("bare host redirect wrong: host=%s url=%s", f.Request.Host, f.Request.URL)
	}
}

func TestNumericOperators(t *testing.T) {
	mk := func(op string, v any, v2 any) bool {
		c := service.InterceptCondition{Type: "response_status", Operator: op, Value: v, Value2: v2}
		r := &service.InterceptRule{
			Enabled: true, LogicOperator: "AND",
			Conditions: []service.InterceptCondition{c},
			Actions:    []service.InterceptAction{{Type: "modify_response_body", Parameters: map[string]any{"responseBodyPattern": "a", "responseBodyReplacement": "b"}}},
		}
		f := reqFlow("GET", "https://x.com/", "x.com", "/")
		f.Response = &flow.Response{Status: 404, Header: map[string][]string{}, Body: []byte("a")}
		engineWith(r).OnResponse(context.Background(), f)
		return string(f.Response.Body) == "b" // body replaced => condition matched
	}
	if !mk("greater_than", "400", nil) {
		t.Error("404 greater_than 400 should match")
	}
	if mk("less_than", "400", nil) {
		t.Error("404 less_than 400 should NOT match")
	}
	if !mk("between", "400", "499") {
		t.Error("404 between 400..499 should match")
	}
	if !mk("in_list", "401,404,500", nil) {
		t.Error("404 in_list should match")
	}
}

func TestMockShortCircuits(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Conditions:    []service.InterceptCondition{cond("url", "regex", `/api/users/\d+$`)},
		Actions: []service.InterceptAction{{Type: "auto_respond", Parameters: map[string]any{
			"response": map[string]any{"status": float64(201), "body": `{"id":1}`, "contentType": "application/json"},
		}}},
	}
	e := engineWith(r)
	f := reqFlow("GET", "https://x.com/api/users/42", "x.com", "/api/users/42")
	d := e.OnRequest(context.Background(), f)
	if d.Kind != flow.Mock {
		t.Fatalf("expected Mock, got %v", d.Kind)
	}
	if f.Response == nil || f.Response.Status != 201 || string(f.Response.Body) != `{"id":1}` {
		t.Fatalf("mock response wrong: %+v", f.Response)
	}
}

func TestSetRequestHeader(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Actions: []service.InterceptAction{{Type: "modify_headers", Parameters: map[string]any{
			"name": "authorization", "value": "Bearer xyz",
		}}},
	}
	e := engineWith(r)
	f := reqFlow("GET", "https://x.com/", "x.com", "/")
	e.OnRequest(context.Background(), f)
	if got := f.Request.Header["Authorization"]; len(got) != 1 || got[0] != "Bearer xyz" {
		t.Fatalf("header not set: %v", f.Request.Header)
	}
}

func TestResponseBodyReplaceAndStatusGuard(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Conditions:    []service.InterceptCondition{cond("response_status", "equals", "200")},
		Actions: []service.InterceptAction{{Type: "modify_response_body", Parameters: map[string]any{
			"responseBodyPattern": "prod", "responseBodyReplacement": "dev",
		}}},
	}
	e := engineWith(r)
	f := reqFlow("GET", "https://x.com/", "x.com", "/")

	// 请求阶段:response 为 nil,response_status 不可评估 → 不应触发(且不 panic)。
	if d := e.OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("request phase should continue, got %v", d.Kind)
	}

	// 响应阶段:状态 200 命中 → 替换 body。
	f.Response = &flow.Response{Status: 200, Header: map[string][]string{}, Body: []byte(`{"env":"prod"}`)}
	e.OnResponse(context.Background(), f)
	if string(f.Response.Body) != `{"env":"dev"}` {
		t.Fatalf("body not replaced: %s", f.Response.Body)
	}

	// 状态 500 不命中 → 不替换。
	f2 := reqFlow("GET", "https://x.com/", "x.com", "/")
	f2.Response = &flow.Response{Status: 500, Header: map[string][]string{}, Body: []byte(`prod`)}
	e.OnResponse(context.Background(), f2)
	if string(f2.Response.Body) != `prod` {
		t.Fatalf("body replaced on non-matching status: %s", f2.Response.Body)
	}
}

func TestOrLogicAndDisabledRuleSkipped(t *testing.T) {
	or := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "OR",
		Conditions: []service.InterceptCondition{
			cond("url_host", "contains", "analytics"),
			cond("url_host", "contains", "doubleclick"),
		},
		Actions: []service.InterceptAction{{Type: "block", Parameters: map[string]any{}}},
	}
	e := engineWith(or)
	f := reqFlow("GET", "https://doubleclick.net/x", "doubleclick.net", "/x")
	if d := e.OnRequest(context.Background(), f); d.Kind != flow.Abort {
		t.Fatalf("OR rule should block doubleclick, got %v", d.Kind)
	}

	// 禁用的规则不应生效。
	or.Enabled = false
	if d := e.OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("disabled rule should not block, got %v", d.Kind)
	}
}

func TestNoConditionsMatchesAll(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Actions:       []service.InterceptAction{{Type: "delay", Parameters: map[string]any{"milliseconds": float64(0)}}},
	}
	e := engineWith(r)
	f := reqFlow("GET", "https://anything.com/", "anything.com", "/")
	if d := e.OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("no-condition rule should continue, got %v", d.Kind)
	}
}
