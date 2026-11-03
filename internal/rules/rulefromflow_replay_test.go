// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package rules

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

// replayEnv 用真实的规则引擎与 pipeline 复现处理器的钩子顺序，检验从会话生成的规则
// 在已有规则与插件共存时是否按 flow/ruleinput.go 的契约复现原请求。
type replayEnv struct {
	t    *testing.T
	svc  *service.Service
	pipe *pipeline.Pipeline
	// cloned 模拟重发 / 构造器：会话存储拿到的是请求开始时的克隆，钩子写在原件上。
	cloned bool
}

func newReplayEnv(t *testing.T, existing ...*service.InterceptRule) *replayEnv {
	svc := service.New(nil, nil, "", "")
	for _, r := range existing {
		svc.CreateRule(r)
	}
	pipe := pipeline.New(nil, nil)
	pipe.RegisterCore(New(svc.Rules))
	return &replayEnv{t: t, svc: svc, pipe: pipe}
}

// run 走一遍请求钩子 → 上游（未被拦截时）→ 响应钩子；beforeComplete 在会话仍进行中时调用。
func (e *replayEnv) run(upstreamBody string, beforeComplete func(f *flow.Flow)) (*flow.Flow, flow.Decision) {
	e.t.Helper()
	ctx := context.Background()
	f := reqFlow("GET", "https://origin.test/api", "origin.test", "/api")
	if e.cloned {
		e.svc.ImportFlowStarted(f.Clone())
	} else {
		e.svc.RecordFlowStarted(f)
	}
	d := e.pipe.OnRequest(ctx, f)
	if beforeComplete != nil {
		beforeComplete(f)
	}
	switch d.Kind {
	case flow.Abort:
		return f, d
	case flow.Mock:
	default:
		if upstreamBody == "" {
			e.t.Fatal("请求不应到达上游")
		}
		f.Response = &flow.Response{Status: 200, Header: map[string][]string{"Content-Type": {"text/plain"}}, Body: []byte(upstreamBody)}
	}
	e.pipe.OnResponse(ctx, f)
	if e.cloned {
		e.svc.ImportFlowCompleted(f.Clone())
	} else {
		e.svc.RecordFlowCompleted(f)
	}
	return f, d
}

var redirectOrigin = &service.InterceptRule{
	Name: "重定向", Enabled: true, Priority: 10,
	Conditions: []service.InterceptCondition{cond("url_host", "equals", "origin.test")},
	Actions:    []service.InterceptAction{{Type: "redirect", Parameters: map[string]any{"url": "https://upstream.test"}, Enabled: true}},
}

// testHook 模拟插件。
type testHook struct{ priority int }

func (h testHook) Name() string      { return "test-hook" }
func (h testHook) Priority() int     { return h.priority }
func (h testHook) Enabled() bool     { return true }
func (h testHook) Match(string) bool { return true }

type reqHook struct {
	testHook
	fn func(f *flow.Flow)
}

func (h reqHook) OnRequest(_ context.Context, f *flow.Flow) flow.Decision {
	h.fn(f)
	return flow.ContinueDecision()
}

type respHook struct {
	testHook
	fn func(f *flow.Flow)
}

func (h respHook) OnResponse(_ context.Context, f *flow.Flow) flow.Decision {
	h.fn(f)
	return flow.ContinueDecision()
}

// 排在规则引擎之前的插件改写路径，生成的规则要按改写后的路径匹配。
var rewriteBeforeEngine = reqHook{testHook{priority: -10}, func(f *flow.Flow) { f.Request.Path = "/rewritten" }}

func TestRuleFromFlowMatchesRuleEngineInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cloned  bool
		pending bool
	}{
		{"已完成会话", false, false},
		{"进行中会话", false, true},
		{"重发已完成", true, false},
		{"重发进行中", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newReplayEnv(t, redirectOrigin)
			e.cloned = tc.cloned
			e.pipe.Register(rewriteBeforeEngine)
			create := func(f *flow.Flow) {
				rule, err := e.svc.CreateRuleFromFlow(f.ID, service.FlowRuleBlock, "屏蔽")
				if err != nil {
					t.Fatal(err)
				}
				if got := rule.Conditions[2].Value; got != "/rewritten" {
					t.Fatalf("路径条件 = %v", got)
				}
			}
			if tc.pending {
				e.run("ok", create)
			} else {
				f, _ := e.run("ok", nil)
				create(f)
			}
			if _, d := e.run("", nil); d.Kind != flow.Abort {
				t.Fatalf("屏蔽规则未命中：%+v", d)
			}
		})
	}
}

// mock 原样回放客户端收到的响应：响应钩子（含原地改写正文的原生插件、依赖改写后请求的规则）不再执行。
func TestMockFromFlowReplaysFinalResponse(t *testing.T) {
	bumpBody := respHook{testHook{priority: 100}, func(f *flow.Flow) {
		for i := range f.Response.Body {
			f.Response.Body[i]++
		}
	}}
	for _, tc := range []struct {
		name  string
		rules []*service.InterceptRule
		hooks []pipeline.Hook
		want  string
	}{
		{
			name: "响应改写不叠加",
			rules: []*service.InterceptRule{{
				Name: "替换", Enabled: true, Priority: 10,
				Actions: []service.InterceptAction{{Type: "modify_response_body", Parameters: map[string]any{
					"responseBodyPattern": "x", "responseBodyReplacement": "xx",
				}, Enabled: true}},
			}},
			want: "xx",
		},
		{
			name: "依赖改写后请求的响应规则",
			rules: []*service.InterceptRule{redirectOrigin, {
				Name: "上游替换", Enabled: true, Priority: 20,
				Conditions: []service.InterceptCondition{cond("url_host", "equals", "upstream.test")},
				Actions: []service.InterceptAction{{Type: "modify_response_body", Parameters: map[string]any{
					"responseBodyPattern": "x", "responseBodyReplacement": "xx",
				}, Enabled: true}},
			}},
			want: "xx",
		},
		{
			name:  "原地改写正文的插件",
			hooks: []pipeline.Hook{bumpBody},
			want:  "y",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newReplayEnv(t, tc.rules...)
			for _, h := range tc.hooks {
				e.pipe.Register(h)
			}
			f, _ := e.run("x", nil)
			if got := string(f.Response.Body); got != tc.want {
				t.Fatalf("原会话响应 = %q, want %q", got, tc.want)
			}
			if _, err := e.svc.CreateRuleFromFlow(f.ID, service.FlowRuleMock, "mock"); err != nil {
				t.Fatal(err)
			}
			replayed, d := e.run("", nil)
			if d.Kind != flow.Mock {
				t.Fatalf("mock 规则未命中：%+v", d)
			}
			if got := string(replayed.Response.Body); got != tc.want {
				t.Fatalf("mock 回放 = %q, want %q", got, tc.want)
			}
		})
	}
}

// HEAD 没有正文，mock 回放要沿用原响应声明的资源长度与编码，h1 与 h2 写回同样（都经 BuildHTTPResponse）。
func TestMockFromHeadFlowKeepsResourceLength(t *testing.T) {
	svc := service.New(nil, nil, "", "")
	svc.StopRecording()
	orig := flow.New(flow.ProtoHTTPS)
	orig.Request = &flow.Request{Method: http.MethodHead, URL: "https://a.test/file", Host: "a.test", Path: "/file", Header: map[string][]string{}}
	orig.Response = &flow.Response{Status: http.StatusOK, Header: map[string][]string{
		"Content-Length": {"125"}, "Content-Encoding": {"gzip"}, "Content-Type": {"application/zip"},
	}}
	orig.State = flow.StateCompleted
	svc.ImportFlowUpdated(orig)
	if _, err := svc.CreateRuleFromFlow(orig.ID, service.FlowRuleMock, "n"); err != nil {
		t.Fatal(err)
	}

	f := flow.New(flow.ProtoHTTPS)
	f.Request = &flow.Request{Method: http.MethodHead, URL: "https://a.test/file", Host: "a.test", Path: "/file", Header: map[string][]string{}}
	if d := New(svc.Rules).OnRequest(context.Background(), f); d.Kind != flow.Mock {
		t.Fatalf("mock 规则未命中：%+v", d)
	}
	req, _ := http.NewRequest(http.MethodHead, "https://a.test/file", nil)
	if h := flow.BuildHTTPResponse(f, req).Header; h.Get("Content-Length") != "125" || h.Get("Content-Encoding") != "gzip" {
		t.Errorf("h2 写回头部 = %v", h)
	}
	var buf bytes.Buffer
	if err := flow.WriteResponse(&buf, f, req); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContentLength != 125 || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("h1 回放 Content-Length = %d, Content-Encoding = %q", resp.ContentLength, resp.Header.Get("Content-Encoding"))
	}
}
