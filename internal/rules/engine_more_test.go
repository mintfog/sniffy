// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package rules

import (
	"context"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// ---- Hook 元信息 ----

func TestHookMetadata(t *testing.T) {
	e := engineWith()
	if e.Name() != "rules-engine" {
		t.Errorf("Name() = %q", e.Name())
	}
	if e.Priority() != 0 {
		t.Errorf("Priority() = %d", e.Priority())
	}
	if !e.Enabled() {
		t.Error("Enabled() should be true")
	}
	if !e.Match("anything") {
		t.Error("Match() should be true")
	}
}

// ---- sortedRules ----

func TestSortedRulesNilProvider(t *testing.T) {
	e := New(nil)
	if got := e.sortedRules(); got != nil {
		t.Fatalf("nil provider should return nil, got %v", got)
	}
	// OnRequest / OnResponse 在无 provider 时也应安全继续。
	f := reqFlow("GET", "https://x.com/", "x.com", "/")
	if d := e.OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("expected Continue, got %v", d.Kind)
	}
}

func TestSortedRulesOrdersByPriorityAndSkipsNilDisabled(t *testing.T) {
	r1 := &service.InterceptRule{Name: "low", Enabled: true, Priority: 10}
	r2 := &service.InterceptRule{Name: "high", Enabled: true, Priority: 1}
	r3 := &service.InterceptRule{Name: "mid", Enabled: true, Priority: 5}
	disabled := &service.InterceptRule{Name: "off", Enabled: false, Priority: 0}
	e := engineWith(r1, nil, r2, disabled, r3)
	got := e.sortedRules()
	if len(got) != 3 {
		t.Fatalf("expected 3 enabled rules, got %d", len(got))
	}
	if got[0].Name != "high" || got[1].Name != "mid" || got[2].Name != "low" {
		t.Fatalf("wrong order: %s %s %s", got[0].Name, got[1].Name, got[2].Name)
	}
}

// ---- fieldValue:覆盖所有字段类型 ----

func TestFieldValueAllTypes(t *testing.T) {
	f := reqFlow("POST", "https://api.example.com/v1/users?id=1&x=2", "api.example.com", "/v1/users")
	f.Request.Header["User-Agent"] = []string{"curl/8.0"}
	f.Request.Header["Content-Type"] = []string{"application/json"}
	f.Request.Header["X-Token"] = []string{"abc"}

	check := func(c service.InterceptCondition, wantVal string, wantOK bool) {
		t.Helper()
		got, ok := fieldValue(c, f)
		if got != wantVal || ok != wantOK {
			t.Errorf("type=%s got (%q,%v) want (%q,%v)", c.Type, got, ok, wantVal, wantOK)
		}
	}

	check(service.InterceptCondition{Type: "url"}, "https://api.example.com/v1/users?id=1&x=2", true)
	check(service.InterceptCondition{Type: "url_host"}, "api.example.com", true)
	check(service.InterceptCondition{Type: "url_path"}, "/v1/users", true)
	check(service.InterceptCondition{Type: "url_query"}, "id=1&x=2", true)
	check(service.InterceptCondition{Type: "method"}, "POST", true)
	check(service.InterceptCondition{Type: "scheme"}, "https", true)
	check(service.InterceptCondition{Type: "request_header", HeaderName: "X-Token"}, "abc", true)
	check(service.InterceptCondition{Type: "user_agent"}, "curl/8.0", true)
	// 无响应时 content_type 取请求头。
	check(service.InterceptCondition{Type: "content_type"}, "application/json", true)
	// 无响应时 response_* 字段不可用。
	check(service.InterceptCondition{Type: "response_status"}, "", false)
	check(service.InterceptCondition{Type: "response_header", HeaderName: "X-Y"}, "", false)
	// 未知字段类型。
	check(service.InterceptCondition{Type: "bogus"}, "", false)

	// 加上响应后,content_type 取响应头,response_* 可用。
	f.Response = &flow.Response{
		Status: 503,
		Header: map[string][]string{"Content-Type": {"text/html"}, "Server": {"nginx"}},
	}
	check(service.InterceptCondition{Type: "content_type"}, "text/html", true)
	check(service.InterceptCondition{Type: "response_status"}, "503", true)
	check(service.InterceptCondition{Type: "response_header", HeaderName: "Server"}, "nginx", true)
}

func TestFieldValueInvalidURL(t *testing.T) {
	f := reqFlow("GET", "http://%zz", "h", "/")
	// url.Parse 失败时 url_query / scheme 仍返回 ("", true)。
	if got, ok := fieldValue(service.InterceptCondition{Type: "url_query"}, f); got != "" || !ok {
		t.Errorf("url_query on bad url = (%q,%v)", got, ok)
	}
	if got, ok := fieldValue(service.InterceptCondition{Type: "scheme"}, f); got != "" || !ok {
		t.Errorf("scheme on bad url = (%q,%v)", got, ok)
	}
}

// ---- evalCondition:Negate ----

func TestEvalConditionNegate(t *testing.T) {
	f := reqFlow("GET", "https://api.example.com/", "api.example.com", "/")
	c := service.InterceptCondition{Type: "url_host", Operator: "contains", Value: "analytics", Negate: true}
	if !evalCondition(c, f, flow.PhaseRequest) {
		t.Error("negated non-match should be true")
	}
	c.Value = "example"
	if evalCondition(c, f, flow.PhaseRequest) {
		t.Error("negated match should be false")
	}
}

// ---- compare:所有操作符 ----

func TestCompareOperators(t *testing.T) {
	cases := []struct {
		op, field, val string
		cs             bool
		want           bool
	}{
		{"equals", "abc", "abc", true, true},
		{"equals", "ABC", "abc", false, true}, // 大小写不敏感
		{"not_equals", "abc", "xyz", true, true},
		{"not_equals", "abc", "abc", true, false},
		{"contains", "hello world", "world", true, true},
		{"not_contains", "hello", "world", true, true},
		{"starts_with", "prefix-x", "prefix", true, true},
		{"ends_with", "x-suffix", "suffix", true, true},
		{"is_empty", "", "", true, true},
		{"is_empty", "x", "", true, false},
		{"not_empty", "x", "", true, true},
		{"not_empty", "", "", true, false},
		{"regex", "user42", `\d+$`, true, true},
		{"regex", "USER", `user`, false, true}, // 大小写不敏感正则
		{"not_regex", "abc", `\d+`, true, true},
		{"regex", "abc", `(`, true, false},    // 非法正则返回 false
		{"unknown_op", "a", "a", true, false}, // 未知操作符
	}
	for _, tc := range cases {
		if got := compare(tc.op, tc.field, tc.val, tc.cs); got != tc.want {
			t.Errorf("compare(%q,%q,%q,cs=%v) = %v want %v", tc.op, tc.field, tc.val, tc.cs, got, tc.want)
		}
	}
}

// ---- matchOp:存在性 / 数值 / 列表 ----

func TestMatchOpExistsAndLists(t *testing.T) {
	mk := func(op string, v, v2 any) service.InterceptCondition {
		return service.InterceptCondition{Operator: op, Value: v, Value2: v2}
	}
	if !matchOp(mk("exists", "", nil), "something") {
		t.Error("exists should be true for non-empty field")
	}
	if matchOp(mk("exists", "", nil), "") {
		t.Error("exists should be false for empty field")
	}
	if !matchOp(mk("not_exists", "", nil), "") {
		t.Error("not_exists should be true for empty field")
	}
	if !matchOp(mk("not_in_list", "a,b,c", nil), "z") {
		t.Error("z not_in_list a,b,c should be true")
	}
	if matchOp(mk("not_in_list", "a,b,c", nil), "b") {
		t.Error("b not_in_list a,b,c should be false")
	}
	// 数值解析失败 → false。
	if matchOp(mk("greater_than", "abc", nil), "10") {
		t.Error("greater_than with non-numeric value should be false")
	}
	if matchOp(mk("less_than", "5", nil), "notnum") {
		t.Error("less_than with non-numeric field should be false")
	}
	if matchOp(mk("between", "x", "y"), "5") {
		t.Error("between with non-numeric bounds should be false")
	}
	// 默认委托 compare。
	if !matchOp(mk("equals", "5", nil), "5") {
		t.Error("equals via default should be true")
	}
}

// ---- applyModifyURL ----

func TestApplyModifyURL(t *testing.T) {
	// 空 pattern → 不改。
	f := reqFlow("GET", "https://a.com/x", "a.com", "/x")
	if applyModifyURL(f, "", "y") {
		t.Error("empty pattern should not change")
	}
	// 非法正则 → 不改。
	if applyModifyURL(f, "(", "y") {
		t.Error("invalid regex should not change")
	}
	// 无匹配 → 不改。
	if applyModifyURL(f, "zzz", "y") {
		t.Error("no match should not change")
	}
	// 命中替换并回填 host/path。
	f2 := reqFlow("GET", "https://old.com/api/v1", "old.com", "/api/v1")
	if !applyModifyURL(f2, `old\.com/api/v1`, "new.com/api/v2") {
		t.Fatal("expected modification")
	}
	if f2.Request.URL != "https://new.com/api/v2" {
		t.Errorf("url = %s", f2.Request.URL)
	}
	if f2.Request.Host != "new.com" || f2.Request.Path != "/api/v2" {
		t.Errorf("host/path not refilled: %s %s", f2.Request.Host, f2.Request.Path)
	}
}

func TestOnRequestModifyURLMethodBody(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Actions: []service.InterceptAction{
			{Type: "modify_url", Parameters: map[string]any{"urlPattern": `users`, "replacement": "people"}},
			{Type: "modify_method", Parameters: map[string]any{"newMethod": "PUT"}},
			{Type: "modify_body", Parameters: map[string]any{"bodyPattern": "prod", "bodyReplacement": "dev"}},
		},
	}
	f := reqFlow("GET", "https://x.com/users", "x.com", "/users")
	if d := engineWith(r).OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("expected Continue, got %v", d.Kind)
	}
	if f.Request.Method != "PUT" {
		t.Errorf("method = %s", f.Request.Method)
	}
	if f.Request.URL != "https://x.com/people" {
		t.Errorf("url = %s", f.Request.URL)
	}
	if string(f.Request.Body) != `{"env":"dev"}` {
		t.Errorf("body = %s", f.Request.Body)
	}
	if !f.Modified {
		t.Error("flow should be modified")
	}
}

// ---- block:自定义状态码(字符串)与消息 ----

func TestBlockWithCustomStatusAndMessage(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Name:          "deny",
		Actions: []service.InterceptAction{{Type: "block", Parameters: map[string]any{
			"statusCode": "503", "message": "go away",
		}}},
	}
	f := reqFlow("GET", "https://x.com/", "x.com", "/")
	d := engineWith(r).OnRequest(context.Background(), f)
	if d.Kind != flow.Abort {
		t.Fatalf("expected Abort, got %v", d.Kind)
	}
	if d.StatusOnAbort != 503 {
		t.Errorf("status = %d", d.StatusOnAbort)
	}
	if d.Reason != "go away" {
		t.Errorf("reason = %q", d.Reason)
	}
}

// ---- modify_headers:设置 Host 同步到 Request.Host ----

func TestModifyHeadersSyncsHost(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Actions: []service.InterceptAction{{Type: "modify_headers", Parameters: map[string]any{
			"name": "Host", "value": "internal.svc",
		}}},
	}
	f := reqFlow("GET", "https://x.com/", "x.com", "/")
	engineWith(r).OnRequest(context.Background(), f)
	if f.Request.Host != "internal.svc" {
		t.Errorf("host not synced: %s", f.Request.Host)
	}
	if got := f.Request.Header["Host"]; len(got) != 1 || got[0] != "internal.svc" {
		t.Errorf("host header = %v", got)
	}
}

// ---- applyHeaderOps:结构化 add/modify/remove ----

func TestApplyHeaderOpsStructured(t *testing.T) {
	h := map[string][]string{"X-Old": {"v"}, "X-Keep": {"k"}}
	params := map[string]any{
		"headers": map[string]any{
			"add":    map[string]any{"X-New": "n"},
			"modify": map[string]any{"X-Keep": "k2"},
			"remove": []any{"X-Old"},
		},
	}
	if !applyHeaderOps(h, params, "headers") {
		t.Fatal("expected changes")
	}
	if got := h["X-New"]; len(got) != 1 || got[0] != "n" {
		t.Errorf("add failed: %v", h["X-New"])
	}
	if got := h["X-Keep"]; len(got) != 1 || got[0] != "k2" {
		t.Errorf("modify failed: %v", h["X-Keep"])
	}
	if _, ok := h["X-Old"]; ok {
		t.Error("remove failed: X-Old still present")
	}
}

func TestApplyHeaderOpsNilGuards(t *testing.T) {
	if applyHeaderOps(nil, map[string]any{"name": "X"}, "headers") {
		t.Error("nil header map should return false")
	}
	if applyHeaderOps(map[string][]string{}, nil, "headers") {
		t.Error("nil params should return false")
	}
	// 既无扁平 name 也无结构化 key → 无改动。
	if applyHeaderOps(map[string][]string{}, map[string]any{"other": 1}, "headers") {
		t.Error("no relevant keys should return false")
	}
}

// ---- OnResponse:modify_status + modify_response_headers ----

func TestOnResponseStatusAndHeaders(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Actions: []service.InterceptAction{
			{Type: "modify_status", Parameters: map[string]any{"statusCode": float64(418)}},
			{Type: "modify_response_headers", Parameters: map[string]any{"name": "X-Cache", "value": "HIT"}},
		},
	}
	f := reqFlow("GET", "https://x.com/", "x.com", "/")
	f.Response = &flow.Response{Status: 200, StatusText: "OK", Header: map[string][]string{}, Body: []byte("body")}
	engineWith(r).OnResponse(context.Background(), f)
	if f.Response.Status != 418 || f.Response.StatusText != "" {
		t.Errorf("status not modified: %d %q", f.Response.Status, f.Response.StatusText)
	}
	if got := f.Response.Header["X-Cache"]; len(got) != 1 || got[0] != "HIT" {
		t.Errorf("response header = %v", got)
	}
	if !f.Modified {
		t.Error("flow should be modified")
	}
}

func TestOnResponseNilResponse(t *testing.T) {
	r := &service.InterceptRule{Enabled: true, Actions: []service.InterceptAction{{Type: "modify_status"}}}
	f := reqFlow("GET", "https://x.com/", "x.com", "/")
	if d := engineWith(r).OnResponse(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("nil response should continue, got %v", d.Kind)
	}
}

func TestOnRequestNilRequest(t *testing.T) {
	f := flow.New(flow.ProtoHTTPS)
	r := &service.InterceptRule{Enabled: true, Actions: []service.InterceptAction{{Type: "block"}}}
	if d := engineWith(r).OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("nil request should continue, got %v", d.Kind)
	}
}

// ---- applyAutoRespond:带响应头 + 默认值 ----

func TestApplyAutoRespondWithHeaders(t *testing.T) {
	r := &service.InterceptRule{
		Enabled:       true,
		LogicOperator: "AND",
		Actions: []service.InterceptAction{{Type: "auto_respond", Parameters: map[string]any{
			"response": map[string]any{
				"status":      float64(202),
				"body":        "hi",
				"contentType": "text/plain",
				"headers":     map[string]any{"X-Mock": "1"},
			},
		}}},
	}
	f := reqFlow("GET", "https://x.com/", "x.com", "/")
	engineWith(r).OnRequest(context.Background(), f)
	if f.Response == nil {
		t.Fatal("response should be set")
	}
	if f.Response.Status != 202 || string(f.Response.Body) != "hi" {
		t.Errorf("resp = %+v", f.Response)
	}
	if got := f.Response.Header["Content-Type"]; len(got) != 1 || got[0] != "text/plain" {
		t.Errorf("content-type = %v", got)
	}
	if got := f.Response.Header["X-Mock"]; len(got) != 1 || got[0] != "1" {
		t.Errorf("custom header = %v", got)
	}
}

func TestApplyAutoRespondDefaults(t *testing.T) {
	e := engineWith()
	f := reqFlow("GET", "https://x.com/", "x.com", "/")
	// 无 response 参数 → 默认 200 / application/json / 空 body。
	e.applyAutoRespond(f, map[string]any{})
	if f.Response.Status != 200 {
		t.Errorf("default status = %d", f.Response.Status)
	}
	if got := f.Response.Header["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
		t.Errorf("default content-type = %v", got)
	}
}

// ---- applyRedirect 边界 ----

func TestApplyRedirectEdgeCases(t *testing.T) {
	// 空目标 → 不改。
	f := reqFlow("GET", "https://a.com/x", "a.com", "/x")
	if applyRedirect(f, "  ") {
		t.Error("empty target should not redirect")
	}
	// 源 URL 非法 → 不改。
	fbad := reqFlow("GET", "http://%zz", "h", "/")
	if applyRedirect(fbad, "http://good.com") {
		t.Error("invalid source url should not redirect")
	}
	// 目标含 path → 整体替换 path。
	f2 := reqFlow("GET", "https://a.com/old?q=1", "a.com", "/old")
	if !applyRedirect(f2, "https://b.com/new") {
		t.Fatal("expected redirect")
	}
	if f2.Request.Path != "/new" || f2.Request.Host != "b.com" {
		t.Errorf("path/host = %s %s", f2.Request.Path, f2.Request.Host)
	}
	// 目标本身非法(含 :// 但内容非法)→ 不改。
	f3 := reqFlow("GET", "https://a.com/x", "a.com", "/x")
	if applyRedirect(f3, "http://%zz") {
		t.Error("invalid target should not redirect")
	}
	// 源 URL 无 scheme 且目标也无 scheme → 默认补 http。
	f4 := reqFlow("GET", "example.com/path", "example.com", "/path")
	if !applyRedirect(f4, "other.com") {
		t.Fatal("expected redirect")
	}
	if f4.Request.Host != "other.com" {
		t.Errorf("host = %s", f4.Request.Host)
	}
	// 目标含 query → 覆盖原 query。
	f5 := reqFlow("GET", "https://a.com/p?old=1", "a.com", "/p")
	if !applyRedirect(f5, "https://b.com/q?new=2") {
		t.Fatal("expected redirect")
	}
	if f5.Request.URL != "https://b.com/q?new=2" {
		t.Errorf("url = %s", f5.Request.URL)
	}
}

// ---- applyBodyReplace 边界 ----

func TestApplyBodyReplaceGuards(t *testing.T) {
	body := []byte("hello")
	if applyBodyReplace(&body, "", "x") {
		t.Error("empty pattern should not change")
	}
	if applyBodyReplace(nil, "a", "b") {
		t.Error("nil body should not change")
	}
	if applyBodyReplace(&body, "zzz", "b") {
		t.Error("no match should not change")
	}
	if !applyBodyReplace(&body, "hello", "world") || string(body) != "world" {
		t.Errorf("replace failed: %s", body)
	}
}

// ---- applyDelay ----

func TestApplyDelay(t *testing.T) {
	// ms <= 0 → 立即返回。
	start := time.Now()
	applyDelay(map[string]any{"milliseconds": float64(0)})
	applyDelay(map[string]any{"milliseconds": float64(-5)})
	if time.Since(start) > 50*time.Millisecond {
		t.Error("non-positive delay should return immediately")
	}
	// 小延迟正常 sleep。
	start = time.Now()
	applyDelay(map[string]any{"milliseconds": float64(5)})
	if time.Since(start) < 3*time.Millisecond {
		t.Error("expected ~5ms delay")
	}
}

// ---- headerValue ----

func TestHeaderValue(t *testing.T) {
	if headerValue(nil, "X") != "" {
		t.Error("nil map should return empty")
	}
	if headerValue(map[string][]string{}, "") != "" {
		t.Error("empty name should return empty")
	}
	h := map[string][]string{"Content-Type": {"a", "b"}, "x-lower": {"v"}}
	// 规范化命中,多值用 ", " 连接。
	if got := headerValue(h, "content-type"); got != "a, b" {
		t.Errorf("canonical hit = %q", got)
	}
	// 大小写不敏感回退(键非规范化形态)。
	if got := headerValue(h, "X-Lower"); got != "v" {
		t.Errorf("case-insensitive fallback = %q", got)
	}
	// 不存在。
	if got := headerValue(h, "Nope"); got != "" {
		t.Errorf("missing = %q", got)
	}
}

// ---- canonicalHeaderKey ----

func TestCanonicalHeaderKey(t *testing.T) {
	cases := map[string]string{
		"content-type":  "Content-Type",
		"X-CUSTOM-Hdr":  "X-Custom-Hdr",
		"host":          "Host",
		"":              "",
		"a--b":          "A--B", // 空段保留
		"authorization": "Authorization",
	}
	for in, want := range cases {
		if got := canonicalHeaderKey(in); got != want {
			t.Errorf("canonicalHeaderKey(%q) = %q want %q", in, got, want)
		}
	}
}

// ---- stringify ----

func TestStringify(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"hi", "hi"},
		{true, "true"},
		{false, "false"},
		{float64(42), "42"},
		{float64(3.5), "3.5"},
		{int(7), "7"},
		{[]int{1}, ""}, // 未支持类型
	}
	for _, tc := range cases {
		if got := stringify(tc.in); got != tc.want {
			t.Errorf("stringify(%v) = %q want %q", tc.in, got, tc.want)
		}
	}
}

// ---- getStr / getInt ----

func TestGetStr(t *testing.T) {
	if getStr(nil, "k") != "" {
		t.Error("nil map should return empty")
	}
	if got := getStr(map[string]any{"k": "v"}, "k"); got != "v" {
		t.Errorf("getStr = %q", got)
	}
}

func TestGetInt(t *testing.T) {
	if getInt(nil, "k") != 0 {
		t.Error("nil map should return 0")
	}
	cases := []struct {
		v    any
		want int
	}{
		{float64(5), 5},
		{int(9), 9},
		{"  12 ", 12},
		{"bad", 0},
		{nil, 0},
		{true, 0}, // 未支持类型
	}
	for _, tc := range cases {
		if got := getInt(map[string]any{"k": tc.v}, "k"); got != tc.want {
			t.Errorf("getInt(%v) = %d want %d", tc.v, got, tc.want)
		}
	}
}

// ---- firstNonEmpty ----

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("firstNonEmpty(a,b) = %q", got)
	}
	if got := firstNonEmpty("", "b"); got != "b" {
		t.Errorf("firstNonEmpty(\"\",b) = %q", got)
	}
}
