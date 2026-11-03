// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/mintfog/sniffy/internal/flow"
)

func TestCreateRuleFromFlowConditionsUseRawPath(t *testing.T) {
	svc := newTestService(t)
	// 浏览器 URL 规范化会把这两种路径都折成 /b；引擎比对的是 Go 的 URL.Path，保留 /a/../b。
	for id, raw := range map[string]string{
		"dots":    "https://Api.Test/a/../b?x=1",
		"encoded": "https://Api.Test/a/%2e%2e/b",
	} {
		svc.RecordFlowCompleted(newFlow(id, withRequest(http.MethodPost, raw)))
		rule, err := svc.CreateRuleFromFlow(id, FlowRuleBlock, "n")
		if err != nil {
			t.Fatal(err)
		}
		want := []InterceptCondition{
			{Type: "method", Operator: "equals", Value: "POST", CaseSensitive: true},
			{Type: "url_host", Operator: "equals", Value: "Api.Test"},
			{Type: "url_path", Operator: "equals", Value: "/a/../b", CaseSensitive: true},
		}
		if !reflect.DeepEqual(rule.Conditions, want) {
			t.Errorf("%s: conditions = %+v", id, rule.Conditions)
		}
		if !rule.Enabled || rule.ID == "" {
			t.Errorf("%s: rule = %+v", id, rule)
		}
	}
}

func TestCreateRuleFromFlowPriorityBeforeExisting(t *testing.T) {
	svc := newTestService(t)
	svc.RecordFlowCompleted(newFlow("f"))
	rule, err := svc.CreateRuleFromFlow("f", FlowRuleDelay, "n")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Priority != 10 {
		t.Errorf("first rule priority = %d, want 10", rule.Priority)
	}
	svc.CreateRule(&InterceptRule{Name: "a", Priority: 40})
	svc.CreateRule(&InterceptRule{Name: "b", Priority: 5})
	rule, err = svc.CreateRuleFromFlow("f", FlowRuleDelay, "n")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Priority != -5 {
		t.Errorf("priority = %d, want -5", rule.Priority)
	}
}

func TestCreateRuleFromFlowMockReplaysHeadersAndBytes(t *testing.T) {
	svc := newTestService(t)
	body := append([]byte("\xEF\xBB\xBF"), "名称,值\n"...)
	svc.RecordFlowCompleted(newFlow("f",
		withResponse(http.StatusFound, "text/csv; charset=utf-8", body),
		withResponseHeader("Set-Cookie", "a=1; Path=/", "b=2; Path=/"),
		withResponseHeader("Location", "/login"),
		withResponseHeader("Access-Control-Allow-Origin", "*"),
		withResponseHeader("Content-Length", "99"),
		withResponseHeader("Content-Encoding", "gzip"),
		withResponseHeader("Connection", "keep-alive"),
		withResponseHeader("X-Raw", "\xff"),
	))
	rule, err := svc.CreateRuleFromFlow("f", FlowRuleMock, "n")
	if err != nil {
		t.Fatal(err)
	}
	// 经 JSON 往返（即落盘与传给前端的形态）后再核对。
	data, _ := json.Marshal(rule)
	var back InterceptRule
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	resp := back.Actions[0].Parameters["response"].(map[string]any)
	if got := resp["body"].(string); !bytes.Equal([]byte(got), body) {
		t.Errorf("body bytes = %q, want %q", got, body)
	}
	if resp["status"].(float64) != http.StatusFound || resp["contentType"] != "text/csv; charset=utf-8" {
		t.Errorf("status/contentType = %v / %v", resp["status"], resp["contentType"])
	}
	want := map[string]any{
		"Set-Cookie":                  []any{"a=1; Path=/", "b=2; Path=/"},
		"Location":                    []any{"/login"},
		"Access-Control-Allow-Origin": []any{"*"},
		"X-Raw":                       []any{"�"},
	}
	if !reflect.DeepEqual(resp["headers"], want) {
		t.Errorf("headers = %#v", resp["headers"])
	}
}

func TestCreateRuleFromFlowMockRejectsUnusableBodies(t *testing.T) {
	svc := newTestService(t)
	streamed := newFlow("streamed", withResponse(http.StatusOK, "text/event-stream", nil))
	streamed.Response.BodyUnbuffered = true
	truncated := newFlow("truncated", withResponse(http.StatusOK, "text/plain", []byte("half")))
	truncated.Response.MarkTruncated()
	trailers := newFlow("trailers", withResponse(http.StatusOK, "text/plain", []byte("ok")))
	trailers.Response.Trailer = map[string][]string{"Digest": {"sha-256=abc"}}
	for _, f := range []*flow.Flow{
		streamed,
		truncated,
		trailers,
		newFlow("no-response"),
		newFlow("binary", withResponse(http.StatusOK, "image/png", []byte{0x89, 'P', 'N', 'G', 0xff})),
		newFlow("large", withResponse(http.StatusOK, "text/plain", []byte(strings.Repeat("a", maxMockBody+1)))),
	} {
		svc.RecordFlowCompleted(f)
	}
	cases := map[string]error{
		"streamed":    ErrMockBodyUnavailable,
		"truncated":   ErrMockBodyUnavailable,
		"trailers":    ErrMockHasTrailers,
		"no-response": ErrMockBodyUnavailable,
		"binary":      ErrMockBodyBinary,
		"large":       ErrMockBodyTooLarge,
		"missing":     ErrFlowNotFound,
	}
	before := len(svc.Rules())
	for id, want := range cases {
		if _, err := svc.CreateRuleFromFlow(id, FlowRuleMock, "n"); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", id, err, want)
		}
	}
	if _, err := svc.CreateRuleFromFlow("binary", "bogus", "n"); !errors.Is(err, ErrUnknownFlowRule) {
		t.Errorf("unknown kind: err = %v", err)
	}
	if got := len(svc.Rules()); got != before {
		t.Errorf("failed creations stored %d rules", got-before)
	}
}

func TestCreateRuleFromFlowEmptyBodyMock(t *testing.T) {
	svc := newTestService(t)
	svc.RecordFlowCompleted(newFlow("f", withResponse(http.StatusNoContent, "", nil)))
	rule, err := svc.CreateRuleFromFlow("f", FlowRuleMock, "n")
	if err != nil {
		t.Fatal(err)
	}
	resp := rule.Actions[0].Parameters["response"].(map[string]any)
	if resp["body"] != "" || resp["status"] != http.StatusNoContent {
		t.Errorf("response = %#v", resp)
	}
	// 显式空串：省略会被引擎补成 application/json，改变原响应语义。
	if ct, ok := resp["contentType"]; !ok || ct != "" {
		t.Errorf("contentType = %#v, want explicit empty string", ct)
	}
}

// 进行中的会话仍由转发 goroutine 改写；建规则只能读已发布的快照（go test -race 守护）。
func TestCreateRuleFromFlowInFlightReadsSnapshot(t *testing.T) {
	svc := newTestService(t)
	f := newFlow("live", withRequest(http.MethodGet, "https://a.test/orig"))
	svc.RecordFlowStarted(f)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			f.Request.Path = "/rewritten"
			f.Request.Method = http.MethodPost
		}
		f.Response = &flow.Response{Status: http.StatusOK, Header: map[string][]string{}, Body: []byte("ok")}
		svc.RecordFlowCompleted(f)
	}()
	for i := 0; i < 50; i++ {
		rule, err := svc.CreateRuleFromFlow("live", FlowRuleBlock, "n")
		if err != nil {
			t.Fatal(err)
		}
		if p := rule.Conditions[2].Value; p != "/orig" && p != "/rewritten" {
			t.Fatalf("path = %v", p)
		}
		if _, err := svc.CreateRuleFromFlow("live", FlowRuleMock, "n"); err != nil && !errors.Is(err, ErrMockFlowPending) {
			t.Fatalf("mock err = %v", err)
		}
	}
	<-done

	if _, err := svc.CreateRuleFromFlow("live", FlowRuleMock, "n"); err != nil {
		t.Fatalf("完成后应可生成 mock: %v", err)
	}
}

// HAR 导入的请求路径须与实时抓包同为 Go 的 URL.Path（不含查询串、已解码），
// 否则由导入会话生成的规则匹配不到同一地址的实时请求。
func TestCreateRuleFromImportedHARMatchesLivePath(t *testing.T) {
	const rawURL = "https://example.com/form%20x/a%2Fb?q=1"
	svc := New(nil, nil, "", "")
	svc.StopRecording()
	har := strings.Replace(browserHAR, "https://example.com/form?q=a+b&q=c", rawURL, 1)
	result, err := svc.ImportSessionArchive(strings.NewReader(har))
	if err != nil || len(result.IDs) != 1 {
		t.Fatalf("导入结果 = %+v, %v", result, err)
	}
	rule, err := svc.CreateRuleFromFlow(result.IDs[0], FlowRuleBlock, "n")
	if err != nil {
		t.Fatal(err)
	}
	live, _ := http.NewRequest(http.MethodPost, rawURL, nil)
	if got := rule.Conditions[2].Value; got != live.URL.Path {
		t.Errorf("path condition = %q, live URL.Path = %q", got, live.URL.Path)
	}
}

// 读取不完整的响应（构造器 / 重发同样经 CaptureResponseToFlowLimit）不能固化成 mock。
func TestCreateRuleFromFlowMockRejectsShortRead(t *testing.T) {
	svc := newTestService(t)
	f := newFlow("short")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Length": {"20"}},
		Body:       io.NopCloser(io.MultiReader(strings.NewReader("half"), iotest.ErrReader(io.ErrUnexpectedEOF))),
	}
	if err := flow.CaptureResponseToFlowLimit(f, resp, 1<<20); err == nil {
		t.Fatal("expected read error")
	}
	f.State = flow.StateErrored
	svc.RecordFlowCompleted(f)
	if _, err := svc.CreateRuleFromFlow("short", FlowRuleMock, "n"); !errors.Is(err, ErrMockBodyUnavailable) {
		t.Fatalf("err = %v, want %v", err, ErrMockBodyUnavailable)
	}
}

// 导入的空路径 URL 按实际请求的 / 生成条件，规则经引擎能命中。
func TestCreateRuleFromImportedHARRootPath(t *testing.T) {
	svc := New(nil, nil, "", "")
	svc.StopRecording()
	har := strings.Replace(browserHAR, "https://example.com/form?q=a+b&q=c", "https://example.com", 1)
	result, err := svc.ImportSessionArchive(strings.NewReader(har))
	if err != nil || len(result.IDs) != 1 {
		t.Fatalf("导入结果 = %+v, %v", result, err)
	}
	rule, err := svc.CreateRuleFromFlow(result.IDs[0], FlowRuleBlock, "n")
	if err != nil {
		t.Fatal(err)
	}
	live, _ := http.NewRequest(http.MethodPost, "https://example.com/", nil)
	if got := rule.Conditions[2].Value; got != live.URL.Path {
		t.Errorf("path condition = %q, live URL.Path = %q", got, live.URL.Path)
	}
}

// 非 UTF-8 的路径写进 rules.json 会被替换成 U+FFFD，重载后不再命中，必须拒绝。
func TestCreateRuleFromFlowRejectsNonUTF8Path(t *testing.T) {
	svc := newTestService(t)
	svc.RecordFlowCompleted(newFlow("bin", withRequest(http.MethodGet, "https://a.test/file%FF")))
	if f, _ := svc.RawFlow("bin"); f.Request.Path != "/file\xff" {
		t.Fatalf("path = %q", f.Request.Path)
	}
	if _, err := svc.CreateRuleFromFlow("bin", FlowRuleBlock, "n"); !errors.Is(err, ErrFlowTargetBinary) {
		t.Fatalf("err = %v, want %v", err, ErrFlowTargetBinary)
	}
}

// 仅大小写不同的同名头合并到规范名下，先原响应的规范写法、后插件追加的值。
func TestCreateRuleFromFlowMockMergesCaseVariantHeaders(t *testing.T) {
	svc := newTestService(t)
	svc.RecordFlowCompleted(newFlow("f",
		withResponse(http.StatusOK, "text/plain", []byte("ok")),
		withResponseHeader("Set-Cookie", "a=1", "b=2"),
		withResponseHeader("set-cookie", "c=3"),
	))
	rule, err := svc.CreateRuleFromFlow("f", FlowRuleMock, "n")
	if err != nil {
		t.Fatal(err)
	}
	headers := rule.Actions[0].Parameters["response"].(map[string]any)["headers"]
	want := map[string]any{"Set-Cookie": []string{"a=1", "b=2", "c=3"}}
	if !reflect.DeepEqual(headers, want) {
		t.Errorf("headers = %#v", headers)
	}
}
