// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package native

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

// 注册表是进程级单例；测试通过快照基线隔离各用例的增量。

// natregHook 实现 pipeline.Hook 基础接口。
type natregHook struct {
	name     string
	priority int
	enabled  bool
	matches  bool
}

func (h *natregHook) Name() string      { return h.name }
func (h *natregHook) Priority() int     { return h.priority }
func (h *natregHook) Enabled() bool     { return h.enabled }
func (h *natregHook) Match(string) bool { return h.matches }

// natregReqHook 额外实现 RequestHook，用于验证阶段接口识别。
type natregReqHook struct {
	natregHook
	decision flow.Decision
	calls    atomic.Int32
}

func (h *natregReqHook) OnRequest(context.Context, *flow.Flow) flow.Decision {
	h.calls.Add(1)
	return h.decision
}

// natregDocHook 对应 registry.go 包注释中的最小原生插件示例。
type natregDocHook struct{}

func (natregDocHook) Name() string      { return "natreg-doc-add-header" }
func (natregDocHook) Priority() int     { return 100 }
func (natregDocHook) Enabled() bool     { return true }
func (natregDocHook) Match(string) bool { return true }

func (natregDocHook) OnResponse(_ context.Context, f *flow.Flow) flow.Decision {
	if f.Response != nil {
		if f.Response.Header == nil {
			f.Response.Header = map[string][]string{}
		}
		f.Response.Header["X-Sniffy-Native"] = []string{"hello"}
		f.Modified = true
	}
	return flow.ContinueDecision()
}

// natregBaseline 保存注册表快照，并在用例结束时恢复该快照。
func natregBaseline(t *testing.T) []pipeline.Hook {
	t.Helper()
	base := All()
	t.Cleanup(func() {
		mu.Lock()
		hooks = append([]pipeline.Hook(nil), base...)
		mu.Unlock()
	})
	return base
}

// natregTail 返回 baseline 之后新增的注册项，并校验原有项顺序。
func natregTail(t *testing.T, baseline []pipeline.Hook) []pipeline.Hook {
	t.Helper()
	cur := All()
	if len(cur) < len(baseline) {
		t.Fatalf("All() 变短: 基线 %d 项,现在 %d 项", len(baseline), len(cur))
	}
	for i := range baseline {
		if cur[i] != baseline[i] {
			t.Fatalf("基线第 %d 项被改写: 期望 %p, 实际 %p", i, baseline[i], cur[i])
		}
	}
	return cur[len(baseline):]
}

func natregNames(hooks []pipeline.Hook) []string {
	out := make([]string, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, h.Name())
	}
	return out
}

// 验证注册表保留登记顺序，供装配层按序 RegisterCore。
func TestNativeRegistryKeepsInsertionOrder(t *testing.T) {
	baseline := natregBaseline(t)

	// 使用逆序优先级区分注册顺序与优先级顺序。
	want := []*natregHook{
		{name: "natreg-order-a", priority: 300, enabled: true, matches: true},
		{name: "natreg-order-b", priority: 200, enabled: false, matches: true},
		{name: "natreg-order-c", priority: 100, enabled: true, matches: false},
	}
	for _, h := range want {
		Register(h)
	}

	got := natregTail(t, baseline)
	if len(got) != len(want) {
		t.Fatalf("新增数量 = %d, 期望 %d (实际新增: %v)", len(got), len(want), natregNames(got))
	}
	for i, h := range want {
		if got[i] != pipeline.Hook(h) {
			t.Fatalf("第 %d 项 = %q, 期望 %q", i, got[i].Name(), h.Name())
		}
		// 注册表返回原钩子及其元数据。
		if got[i].Priority() != h.priority || got[i].Enabled() != h.enabled || got[i].Match("http://x") != h.matches {
			t.Fatalf("第 %d 项元数据失真: priority=%d enabled=%v match=%v, 期望 %d/%v/%v",
				i, got[i].Priority(), got[i].Enabled(), got[i].Match("http://x"), h.priority, h.enabled, h.matches)
		}
	}
}

// 验证 All() 返回独立副本，调用方修改快照不影响全局注册表。
func TestNativeRegistryAllReturnsIsolatedCopy(t *testing.T) {
	baseline := natregBaseline(t)
	own := &natregHook{name: "natreg-copy-owner", priority: 10, enabled: true, matches: true}
	Register(own)

	first := All()
	second := All()
	idx := len(first) - 1
	if first[idx] != pipeline.Hook(own) {
		t.Fatalf("末项 = %q, 期望 %q", first[idx].Name(), own.name)
	}

	intruder := &natregHook{name: "natreg-copy-intruder", priority: 20, enabled: true, matches: true}
	first[idx] = intruder

	if second[idx] != pipeline.Hook(own) {
		t.Fatalf("两次 All() 共享底层数组: 第二份末项变成 %q", second[idx].Name())
	}
	after := natregTail(t, baseline)
	if len(after) != 1 {
		t.Fatalf("注册表新增 %d 项, 期望 1 项: %v", len(after), natregNames(after))
	}
	if after[0] != pipeline.Hook(own) {
		t.Fatalf("注册表内容被副本改写: 末项 = %q, 期望 %q", after[0].Name(), own.name)
	}
}

// 验证 Register 与 All 并发访问时保持互斥，注册项完整且唯一。
func TestNativeRegistryConcurrentRegisterAndAll(t *testing.T) {
	baseline := natregBaseline(t)

	const writers = 32
	const readers = 8

	start := make(chan struct{})
	var wg sync.WaitGroup
	var snapshotsMu sync.Mutex
	var snapshots [][]pipeline.Hook

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			Register(&natregHook{name: fmt.Sprintf("natreg-conc-%02d", i), priority: i, enabled: true, matches: true})
		}(i)
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			local := make([][]pipeline.Hook, 0, 16)
			for j := 0; j < 16; j++ {
				snap := All()
				for k, h := range snap {
					if h == nil {
						t.Errorf("并发快照第 %d 项为 nil", k)
						return
					}
				}
				local = append(local, snap)
			}
			snapshotsMu.Lock()
			snapshots = append(snapshots, local...)
			snapshotsMu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	final := natregTail(t, baseline)
	if len(final) != writers {
		t.Fatalf("并发注册后新增 %d 项, 期望 %d 项", len(final), writers)
	}
	seen := make(map[string]int, writers)
	for _, h := range final {
		seen[h.Name()]++
	}
	for i := 0; i < writers; i++ {
		name := fmt.Sprintf("natreg-conc-%02d", i)
		if seen[name] != 1 {
			t.Fatalf("%s 出现 %d 次, 期望 1 次", name, seen[name])
		}
	}

	// 每个并发快照均应是最终注册序列的前缀。
	full := All()
	for _, snap := range snapshots {
		if len(snap) > len(full) {
			t.Fatalf("快照长度 %d 超过最终长度 %d", len(snap), len(full))
		}
		for i := range snap {
			if snap[i] != full[i] {
				t.Fatalf("并发快照第 %d 项 = %q, 与最终状态的 %q 不符", i, snap[i].Name(), full[i].Name())
			}
		}
	}
}

// 验证注册表保留钩子动态类型，装配层可按阶段接口挂载。
func TestNativeRegistryPreservesStageTypeIdentity(t *testing.T) {
	baseline := natregBaseline(t)
	rh := &natregReqHook{
		natregHook: natregHook{name: "natreg-stage-req", priority: 5, enabled: true, matches: true},
		decision:   flow.AbortDecision(418, "natreg-abort"),
	}
	Register(rh)

	tail := natregTail(t, baseline)
	if len(tail) != 1 {
		t.Fatalf("新增 %d 项, 期望 1 项: %v", len(tail), natregNames(tail))
	}
	got := tail[0]
	if got != pipeline.Hook(rh) {
		t.Fatalf("取回的不是登记的那个值: %q", got.Name())
	}
	// 按 bootstrap 流程遍历 All() 并 RegisterCore，验证原生插件处置生效。
	pipe := pipeline.New(nil, nil)
	pipe.RegisterCore(got)
	f := &flow.Flow{ID: "natreg-1", Request: &flow.Request{Method: "GET", URL: "http://example.com/x"}}
	d := pipe.OnRequest(context.Background(), f)
	if d.Kind != flow.Abort || d.StatusOnAbort != 418 || d.Reason != "natreg-abort" {
		t.Fatalf("管道返回 %v/%d/%q, 期望 abort/418/natreg-abort", d.Kind, d.StatusOnAbort, d.Reason)
	}
	if n := rh.calls.Load(); n != 1 {
		t.Fatalf("OnRequest 被调用 %d 次, 期望 1 次", n)
	}
	// 仅实现请求阶段接口的钩子不参与响应阶段。
	f.Response = &flow.Response{Status: 200}
	if d := pipe.OnResponse(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("响应阶段返回 %v, 期望 continue", d.Kind)
	}
	if n := rh.calls.Load(); n != 1 {
		t.Fatalf("响应阶段后调用次数 = %d, 期望仍为 1", n)
	}
}

// 验证原生插件从接口实现、Register 到 All/RegisterCore 的完整装配链路。
func TestNativeRegistryDocumentedRecipeTakesEffect(t *testing.T) {
	baseline := natregBaseline(t)
	Register(natregDocHook{})

	tail := natregTail(t, baseline)
	if len(tail) != 1 || tail[0].Name() != "natreg-doc-add-header" {
		t.Fatalf("新增项 = %v, 期望仅 natreg-doc-add-header", natregNames(tail))
	}

	pipe := pipeline.New(nil, nil)
	for _, h := range tail {
		pipe.RegisterCore(h)
	}
	f := &flow.Flow{
		ID:       "natreg-2",
		Request:  &flow.Request{Method: "GET", URL: "http://example.com/y"},
		Response: &flow.Response{Status: 200},
	}
	if d := pipe.OnResponse(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("处置 = %v, 期望 continue", d.Kind)
	}
	if got := f.Response.Header["X-Sniffy-Native"]; len(got) != 1 || got[0] != "hello" {
		t.Fatalf("响应头 X-Sniffy-Native = %v, 期望 [hello]", got)
	}
	if !f.Modified {
		t.Fatal("插件就地改写了响应,Flow.Modified 必须为 true")
	}
}
