// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/plugin/js"
)

func TestHTTPExecutionOrderAndShortCircuit(t *testing.T) {
	p := New(nil, nil)
	p.Register(&reqHook{stubHook: stubHook{name: "禁用", disabled: true}})
	p.Register(&reqHook{stubHook: stubHook{name: "未匹配", matchFn: func(string) bool { return false }}})
	p.Register(&reqHook{stubHook: stubHook{name: "首个", priority: 10}})
	p.Register(&reqHook{
		stubHook: stubHook{name: "阻断", priority: 20},
		fn:       func(*flow.Flow) flow.Decision { return flow.AbortDecision(403, "阻断") },
	})
	p.Register(&reqHook{stubHook: stubHook{name: "后续", priority: 30}})
	f := newReqFlow()
	p.OnRequest(context.Background(), f)
	want := []flow.Execution{
		{Kind: "plugin", ID: "首个", Name: "首个", Phase: flow.PhaseRequest},
		{Kind: "plugin", ID: "阻断", Name: "阻断", Phase: flow.PhaseRequest},
	}
	if !reflect.DeepEqual(f.Executions, want) {
		t.Fatalf("执行记录 = %+v, want %+v", f.Executions, want)
	}
}

func TestJSHTTPExecutionRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		phases []flow.Phase
	}{
		{"空脚本", "", nil},
		{"仅请求", "function onRequest(f) {}", []flow.Phase{flow.PhaseRequest}},
		{"仅响应", "function onResponse(f) {}", []flow.Phase{flow.PhaseResponse}},
		{"两个阶段", "function onRequest(f) {} function onResponse(f) {}", []flow.Phase{flow.PhaseRequest, flow.PhaseResponse}},
		{"抛错", "function onRequest(f) { throw new Error('测试错误'); }", []flow.Phase{flow.PhaseRequest}},
		{"超时", "function onRequest(f) { while (true) {} }", []flow.Phase{flow.PhaseRequest}},
		{"动态钩子", "function onRequest(f) { onResponse = function(f) {}; }", []flow.Phase{flow.PhaseRequest, flow.PhaseResponse}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plugin, err := js.NewPlugin(js.Config{ID: "plugin-id", Name: "测试插件", Enabled: true, Source: tc.source, Timeout: 20 * time.Millisecond}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(plugin.Close)
			p := New(nil, nil)
			p.Register(plugin)
			f := newRespFlow()
			ctx := context.Background()
			p.OnRequest(ctx, f)
			p.OnResponse(ctx, f)
			if len(f.Executions) != len(tc.phases) {
				t.Fatalf("执行记录 = %+v, want 阶段 %v", f.Executions, tc.phases)
			}
			for i, phase := range tc.phases {
				want := flow.Execution{Kind: "plugin", ID: "plugin-id", Name: "测试插件", Phase: phase}
				if f.Executions[i] != want {
					t.Errorf("执行记录[%d] = %+v, want %+v", i, f.Executions[i], want)
				}
			}
		})
	}
}

func TestHTTPExecutionsAtBreakpoint(t *testing.T) {
	p := New(nil, nil)
	p.Register(&reqHook{stubHook: stubHook{name: "请求插件"}})
	p.RegisterCore(&respHook{stubHook: stubHook{name: "响应插件"}})
	p.Breakpoints().SetGlobalBreak(false, true)
	f := newRespFlow()
	p.OnRequest(context.Background(), f)
	done := runAsync(func() flow.Decision { return p.OnResponse(context.Background(), f) })
	waitPaused(t, p.Breakpoints(), f.ID)
	snapshots := p.Breakpoints().List()
	want := []flow.Execution{
		{Kind: "plugin", ID: "请求插件", Name: "请求插件", Phase: flow.PhaseRequest},
		{Kind: "plugin", ID: "响应插件", Name: "响应插件", Phase: flow.PhaseResponse},
	}
	if len(snapshots) != 1 || !reflect.DeepEqual(snapshots[0].Executions, want) {
		t.Errorf("暂停快照 = %+v, want 执行记录 %+v", snapshots, want)
	}
	if err := p.Breakpoints().Resume(f.ID, nil); err != nil {
		t.Fatal(err)
	}
	waitDecision(t, done)
}

func BenchmarkHTTPExecutions(b *testing.B) {
	for _, kind := range []string{"none", "native", "js"} {
		b.Run(kind, func(b *testing.B) {
			p := New(nil, nil)
			switch kind {
			case "native":
				p.Register(&reqHook{stubHook: stubHook{name: "native"}})
			case "js":
				plugin, err := js.NewPlugin(js.Config{ID: "js", Name: "测试插件", Enabled: true, Source: "function onRequest(f) {}"}, nil)
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(plugin.Close)
				p.Register(plugin)
			}
			ctx := context.Background()
			req := flow.Request{URL: "https://example.test/", Method: "GET"}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				f := flow.Flow{Request: &req}
				p.OnRequest(ctx, &f)
			}
		})
	}
}
