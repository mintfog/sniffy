// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"context"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

// fakeHook 是一个记录调用次数的请求钩子。
type fakeHook struct {
	name     string
	priority int
	calls    *int
}

func (h fakeHook) Name() string      { return h.name }
func (h fakeHook) Priority() int     { return h.priority }
func (h fakeHook) Enabled() bool     { return true }
func (h fakeHook) Match(string) bool { return true }
func (h fakeHook) OnRequest(_ context.Context, _ *flow.Flow) flow.Decision {
	*h.calls++
	return flow.ContinueDecision()
}

func newReqFlow() *flow.Flow {
	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{Method: "GET", URL: "https://x.com/", Host: "x.com", Path: "/", Header: map[string][]string{}}
	return f
}

// 核心钩子(RegisterCore)必须在 Clear()(插件热重载)后仍然生效,插件钩子则被清除。
func TestCoreHookSurvivesClear(t *testing.T) {
	p := New(nil, nil)
	var coreCalls, pluginCalls int
	p.RegisterCore(fakeHook{name: "core", priority: 0, calls: &coreCalls})
	p.Register(fakeHook{name: "plugin", priority: 1, calls: &pluginCalls})

	p.OnRequest(context.Background(), newReqFlow())
	if coreCalls != 1 || pluginCalls != 1 {
		t.Fatalf("before clear: core=%d plugin=%d, want 1/1", coreCalls, pluginCalls)
	}

	p.Clear() // 模拟插件热重载

	p.OnRequest(context.Background(), newReqFlow())
	if coreCalls != 2 {
		t.Fatalf("core hook should survive Clear(): core=%d, want 2", coreCalls)
	}
	if pluginCalls != 1 {
		t.Fatalf("plugin hook should be cleared: plugin=%d, want 1", pluginCalls)
	}
}

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pattern, url string
		want         bool
	}{
		{"https://api.sniffy.dev/v1/*", "https://api.sniffy.dev/v1/orders", true},
		{"https://api.sniffy.dev/v1/*", "https://api.sniffy.dev/v2/orders", false},
		{"https://*.example.com/checkout", "https://shop.example.com/checkout", true},
		{"https://*.example.com/checkout", "https://example.com/checkout", false},
		{"analytics", "https://analytics.google.com/x", true}, // 无 * 时子串匹配
		{"", "https://anything", false},                       // 空模式不匹配
		{"*", "https://anything", true},
		{"https://a.com/*.json", "https://a.com/data/x.json", true},
		{"https://a.com/*.json", "https://a.com/data/x.txt", false},
	}
	for _, c := range cases {
		if got := wildcardMatch(c.pattern, c.url); got != c.want {
			t.Errorf("wildcardMatch(%q, %q) = %v, want %v", c.pattern, c.url, got, c.want)
		}
	}
}

func TestShouldBreakForURLRule(t *testing.T) {
	bm := NewBreakpointManager(nil)
	r := bm.AddRule("https://api.x.com/*", true, false)
	if r.ID == "" {
		t.Fatal("AddRule should return a rule with an ID")
	}
	if !bm.ShouldBreakFor("https://api.x.com/v1", flow.PhaseRequest) {
		t.Error("request-phase rule should break on matching URL")
	}
	if bm.ShouldBreakFor("https://api.x.com/v1", flow.PhaseResponse) {
		t.Error("rule only covers request phase; response should not break")
	}
	if bm.ShouldBreakFor("https://other.com/v1", flow.PhaseRequest) {
		t.Error("non-matching URL should not break")
	}
	// 禁用后不应触发。
	bm.ToggleRule(r.ID, false)
	if bm.ShouldBreakFor("https://api.x.com/v1", flow.PhaseRequest) {
		t.Error("disabled rule should not break")
	}
	// 删除后列表为空。
	bm.DeleteRule(r.ID)
	if len(bm.ListRules()) != 0 {
		t.Error("rule should be deleted")
	}
	// 全局开关仍独立生效。
	bm.SetGlobalBreak(false, true)
	if !bm.ShouldBreakFor("https://whatever", flow.PhaseResponse) {
		t.Error("global response break should apply to any URL")
	}
}
