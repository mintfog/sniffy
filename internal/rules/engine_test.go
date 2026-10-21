// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package rules

import (
	"context"
	"strconv"
	"strings"
	"sync"
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

func TestBodyRegexReplacement(t *testing.T) {
	cases := []struct {
		name, body, pattern, replacement, want string
	}{
		{"正则元字符", `a.b aXb`, `a.b`, `new`, `new new`},
		{"字面字符转义", `a.b aXb`, `a\.b`, `$$1`, `$1 aXb`},
		{"全部匹配与编号捕获组", `id=12 id=34`, `id=(\d+)`, `user=${1}`, `user=12 user=34`},
		{"命名捕获组与字面美元符", `price=12`, `price=(?P<amount>\d+)`, `$$${amount}`, `$12`},
		{"价格正则替换", `price=12.00`, `price=(?P<amount>\d+)\.00`, `$$${amount}.99`, `$12.99`},
		{"忽略大小写", `PROD prod`, `(?i)prod`, `dev`, `dev dev`},
		{"跨行匹配", "<p>first\nsecond</p>", `(?s)<p>.*?</p>`, `<p>dev</p>`, `<p>dev</p>`},
		{"空替换值删除匹配", `a1b2`, `\d+`, ``, `ab`},
		{"非法表达式保留原文", `original`, `(`, `new`, `original`},
		{"不支持环视", `original`, `(?=original)`, `new`, `original`},
		{"没有匹配", `original`, `\d+`, `new`, `original`},
		{"结果相同", `original`, `original`, `$0`, `original`},
		{"空表达式保留原文", `original`, ``, `new`, `original`},
		{"空响应体", ``, `^$`, `new`, `new`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := engineWith(&service.InterceptRule{
				Enabled: true,
				Actions: []service.InterceptAction{
					{Type: "modify_body", Parameters: map[string]any{"bodyPattern": tc.pattern, "bodyReplacement": tc.replacement}},
					{Type: "modify_response_body", Parameters: map[string]any{"responseBodyPattern": tc.pattern, "responseBodyReplacement": tc.replacement}},
				},
			})
			f := &flow.Flow{
				Request:  &flow.Request{Body: []byte(tc.body)},
				Response: &flow.Response{Body: []byte(tc.body)},
			}
			wantModified := tc.body != tc.want
			e.OnRequest(context.Background(), f)
			if string(f.Request.Body) != tc.want || f.Modified != wantModified {
				t.Errorf("请求 Body=%q Modified=%v, want %q/%v", f.Request.Body, f.Modified, tc.want, wantModified)
			}
			f.Modified = false
			e.OnResponse(context.Background(), f)
			if string(f.Response.Body) != tc.want || f.Modified != wantModified {
				t.Errorf("响应 Body=%q Modified=%v, want %q/%v", f.Response.Body, f.Modified, tc.want, wantModified)
			}
		})
	}
}

func TestResponseBodyReplaceAvailability(t *testing.T) {
	cases := []struct {
		name       string
		metadata   map[string]any
		unbuffered bool
		wantBody   string
	}{
		{"普通空正文", nil, false, "prefix"},
		{"SSE 模式缓冲正文", map[string]any{"stream": flow.StreamSSE}, false, "prefix"},
		{"SSE", map[string]any{"stream": flow.StreamSSE}, true, ""},
		{"gRPC", map[string]any{"stream": flow.StreamGRPC}, true, ""},
		{"分块流", map[string]any{"stream": flow.StreamChunk}, true, ""},
		{"透传", map[string]any{"passthrough": true}, true, ""},
		{"未缓冲正文", nil, true, ""},
	}
	e := engineWith(&service.InterceptRule{
		Enabled: true,
		Actions: []service.InterceptAction{{
			Type: "modify_response_body",
			Parameters: map[string]any{
				"responseBodyPattern": "^", "responseBodyReplacement": "prefix",
			},
		}},
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := reqFlow("GET", "https://example.test/", "example.test", "/")
			f.Metadata = tc.metadata
			f.Response = &flow.Response{Status: 200, BodyUnbuffered: tc.unbuffered}
			e.OnResponse(context.Background(), f)
			if string(f.Response.Body) != tc.wantBody {
				t.Errorf("Body = %q, want %q", f.Response.Body, tc.wantBody)
			}
			wantModified := tc.wantBody != ""
			if f.Modified != wantModified {
				t.Errorf("Modified = %v, want %v", f.Modified, wantModified)
			}
			if !wantModified && len(f.Executions) != 0 {
				t.Errorf("跳过正文替换后仍记录规则执行：%+v", f.Executions)
			}
		})
	}
}

func TestUnbufferedResponseHeaderActions(t *testing.T) {
	e := engineWith(&service.InterceptRule{
		Enabled: true,
		Actions: []service.InterceptAction{
			{Type: "modify_response_body", Parameters: map[string]any{"responseBodyPattern": "^", "responseBodyReplacement": "prefix"}},
			{Type: "modify_status", Parameters: map[string]any{"statusCode": 201}},
			{Type: "modify_response_headers", Parameters: map[string]any{"name": "X-Test", "value": "changed"}},
		},
	})
	for _, metadata := range []map[string]any{{"stream": flow.StreamSSE}, {"passthrough": true}} {
		f := reqFlow("GET", "https://example.test/", "example.test", "/")
		f.Metadata = metadata
		f.Response = &flow.Response{Status: 200, Header: map[string][]string{}, BodyUnbuffered: true}
		e.OnResponse(context.Background(), f)
		if f.Response.Body != nil {
			t.Errorf("metadata=%v, Body = %q, want nil", metadata, f.Response.Body)
		}
		if f.Response.Status != 201 || headerValue(f.Response.Header, "X-Test") != "changed" || !f.Modified {
			t.Errorf("metadata=%v, 头部动作未生效：response=%+v Modified=%v", metadata, f.Response, f.Modified)
		}
		if len(f.Executions) != 1 {
			t.Errorf("metadata=%v, 执行记录 = %+v, want 1", metadata, f.Executions)
		}
	}
}

func TestStreamRequestBodyReplaceSkipped(t *testing.T) {
	e := engineWith(&service.InterceptRule{
		Enabled: true,
		Actions: []service.InterceptAction{
			{Type: "modify_body", Parameters: map[string]any{"bodyPattern": "^", "bodyReplacement": "prefix"}},
			{Type: "replace_body", Parameters: map[string]any{"body": "完整替换"}},
			{Type: "modify_method", Parameters: map[string]any{"newMethod": "POST"}},
		},
	})
	f := reqFlow("GET", "https://example.test/", "example.test", "/")
	f.Metadata["stream"] = flow.StreamGRPC
	f.Request.Body = nil
	f.Request.BodyUnbuffered = true
	e.OnRequest(context.Background(), f)
	if f.Request.Body != nil {
		t.Errorf("流式请求 Body = %q, want nil", f.Request.Body)
	}
	if f.Request.Method != "POST" || !f.Modified {
		t.Errorf("请求头级动作未生效：method=%s Modified=%v", f.Request.Method, f.Modified)
	}
}

func TestBodyRegexRuleUpdates(t *testing.T) {
	var current *service.InterceptRule
	e := New(func() []*service.InterceptRule { return []*service.InterceptRule{current} })
	for _, tc := range []struct {
		pattern, replacement, want string
	}{
		{"prod", "dev", "dev staging"},
		{"staging", "test", "prod test"},
		{"staging", "local", "prod local"},
		{"(", "new", "prod staging"},
		{"(prod)", "${1}-local", "prod-local staging"},
	} {
		current = &service.InterceptRule{
			Enabled: true,
			Actions: []service.InterceptAction{{
				Type: "modify_response_body",
				Parameters: map[string]any{
					"responseBodyPattern": tc.pattern, "responseBodyReplacement": tc.replacement,
				},
			}},
		}
		f := &flow.Flow{Response: &flow.Response{Body: []byte("prod staging")}}
		e.OnResponse(context.Background(), f)
		if string(f.Response.Body) != tc.want {
			t.Errorf("pattern=%q replacement=%q, Body=%q, want %q", tc.pattern, tc.replacement, f.Response.Body, tc.want)
		}
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

func TestRegexpCacheReuse(t *testing.T) {
	var cache regexpCache
	first := cache.get("a+")
	if first == nil || !first.MatchString("aaa") {
		t.Fatal("有效正则未编译")
	}
	if cache.get("a+") != first {
		t.Error("重复访问未复用已编译的正则")
	}
	if cache.get("(") != nil {
		t.Fatal("非法正则应返回 nil")
	}
	if value, ok := cache.items["("]; !ok || value != nil {
		t.Error("非法正则未缓存")
	}
}

func TestRegexpCacheEviction(t *testing.T) {
	var cache regexpCache
	first := cache.get("first")
	for i := range regexpCacheCapacity {
		value := "item" + strconv.Itoa(i)
		re := cache.get("^" + value + "$")
		if re == nil || !re.MatchString(value) {
			t.Fatalf("正则缓存返回错误匹配结果：%s", value)
		}
	}
	if len(cache.items) != regexpCacheCapacity {
		t.Errorf("缓存条数 = %d, want %d", len(cache.items), regexpCacheCapacity)
	}
	if cache.get("first") == first {
		t.Error("缓存超出容量后未淘汰最先插入的正则")
	}
	if len(cache.items) != regexpCacheCapacity {
		t.Error("重新编译已淘汰的正则后缓存超出容量")
	}
}

func TestRegexpCacheConcurrent(t *testing.T) {
	var cache regexpCache
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for i := range 512 {
				value := "item" + strconv.Itoa((worker*128+i)%512)
				re := cache.get("^" + value + "$")
				if re == nil || !re.MatchString(value) {
					t.Errorf("并发访问返回错误匹配结果：%s", value)
					return
				}
			}
		})
	}
	workers.Wait()
	if len(cache.items) > regexpCacheCapacity {
		t.Errorf("并发访问后缓存条数 = %d, want <= %d", len(cache.items), regexpCacheCapacity)
	}
}

func BenchmarkResponseBodyReplace(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{
		{"8KiB", 8 << 10},
		{"1MiB", 1 << 20},
	} {
		b.Run(size.name, func(b *testing.B) {
			body := []byte(strings.Repeat("x", size.n-14) + `{"env":"prod"}`)
			e := engineWith(&service.InterceptRule{
				Enabled: true,
				Actions: []service.InterceptAction{{
					Type: "modify_response_body",
					Parameters: map[string]any{
						"responseBodyPattern":     `("env"\s*:\s*")[^"]*(")`,
						"responseBodyReplacement": `${1}dev${2}`,
					},
				}},
			})
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				f := flow.Flow{Response: &flow.Response{Body: body}}
				e.OnResponse(ctx, &f)
			}
		})
	}
}

func BenchmarkBodyReplaceNoMatch(b *testing.B) {
	body := []byte(strings.Repeat("x", 256))
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		current := body
		applyBodyReplace(&current, "needle", "replacement")
	}
}

func BenchmarkBodyReplaceNoMatchParallel(b *testing.B) {
	body := []byte(strings.Repeat("x", 256))
	current := body
	applyBodyReplace(&current, "needle", "replacement")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			current := body
			applyBodyReplace(&current, "needle", "replacement")
		}
	})
}
