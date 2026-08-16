// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// editedURL 是各用例模拟「UI 编辑后放行」时写入的请求 URL。
const editedURL = "https://edited.example/"

// eventSink 捕获断点管理器广播的事件。
type eventSink struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	typ     string
	payload any
}

func (s *eventSink) emit(typ string, payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, capturedEvent{typ: typ, payload: payload})
}

func (s *eventSink) snapshot() []capturedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capturedEvent(nil), s.events...)
}

func (s *eventSink) types() string {
	var out []string
	for _, e := range s.snapshot() {
		out = append(out, e.typ)
	}
	return strings.Join(out, ",")
}

// waitTypes 轮询等待事件序列达到 want。不能用 waitPaused 代替:Pause 先发布再在锁外 emit,
// 两者无 happens-before,waitPaused 返回时事件可能尚未发出。
func (s *eventSink) waitTypes(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.types() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待事件序列超时: got %q, want %q", s.types(), want)
}

// ---- 构造与默认值 ----

// 超时与并发上限是生产环境的两道兜底,默认值被改动应当被测试拦下。
func TestNewBreakpointManagerDefaults(t *testing.T) {
	bm := NewBreakpointManager(nil)
	if bm.timeout != 5*time.Minute {
		t.Errorf("默认超时 = %v, want 5m", bm.timeout)
	}
	if bm.maxOpen != 100 {
		t.Errorf("默认并发上限 = %d, want 100", bm.maxOpen)
	}
	if bm.emit == nil {
		t.Error("emit 为 nil 时应替换为空实现,避免 Pause 时空指针")
	}
}

// ---- Pause / Resume / Abort ----

// 带编辑放行:只合并 Request/Response,并把 flow 标记为 Modified、恢复暂停前状态。
func TestPauseResumeMergesRequestAndResponseOnly(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newRespFlow()
	f.State = flow.StateAwaitingResponse
	f.Tags = []string{"原始标签"}

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseResponse) }()
	waitPaused(t, bm, f.ID)

	edited := f.Clone()
	edited.Request.URL = "https://edited.example/"
	edited.Response.Status = 418
	edited.Tags = []string{"被改的标签"}  // 不在合并范围内
	edited.State = flow.StateErrored // 不在合并范围内

	if !bm.Resume(f.ID, edited) {
		t.Fatal("Resume 应成功")
	}

	select {
	case abort := <-done:
		if abort {
			t.Fatal("Resume 不应返回阻断")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Pause 未在超时前返回")
	}

	if f.Request.URL != "https://edited.example/" {
		t.Errorf("请求未合并: URL = %q", f.Request.URL)
	}
	if f.Response.Status != 418 {
		t.Errorf("响应未合并: Status = %d, want 418", f.Response.Status)
	}
	if !f.Modified {
		t.Error("带编辑放行应标记 Modified")
	}
	if got := strings.Join(f.Tags, ","); got != "原始标签" {
		t.Errorf("Tags 不在合并范围内, got %q", got)
	}
	if f.State != flow.StateAwaitingResponse {
		t.Errorf("状态 = %q, want %q(恢复暂停前的状态,而非编辑值)", f.State, flow.StateAwaitingResponse)
	}
	if n := len(bm.List()); n != 0 {
		t.Errorf("放行后暂停列表应为空, got %d", n)
	}
}

// 不带编辑放行:flow 内容与 Modified 标记都不应被动到。
func TestPauseResumeWithoutEditsKeepsFlowIntact(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newReqFlow()

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseRequest) }()
	waitPaused(t, bm, f.ID)

	if !bm.Resume(f.ID, nil) {
		t.Fatal("Resume 应成功")
	}
	if abort := <-done; abort {
		t.Fatal("Resume 不应返回阻断")
	}

	if f.Modified {
		t.Error("未编辑时不应标记 Modified")
	}
	if f.Request.URL != "https://x.com/" {
		t.Errorf("URL 不应被改动, got %q", f.Request.URL)
	}
}

// Abort 返回 true 交由调用方翻译成阻断;状态刻意不回滚,终态由处理器设置。
func TestPauseAbortReturnsTrue(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newReqFlow()

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseRequest) }()
	waitPaused(t, bm, f.ID)

	if !bm.Abort(f.ID) {
		t.Fatal("Abort 应成功")
	}
	if abort := <-done; !abort {
		t.Fatal("Pause 应返回 true(阻断)")
	}
	if f.State != flow.StatePausedAtBreakpoint {
		t.Errorf("状态 = %q, want %q(阻断路径不回滚状态)", f.State, flow.StatePausedAtBreakpoint)
	}
	if n := len(bm.List()); n != 0 {
		t.Errorf("阻断后暂停列表应为空, got %d", n)
	}
}

// 超时必须失败开放:放行未编辑的 flow、恢复状态,并在 Metadata 上留痕。
func TestPauseTimeoutFailsOpen(t *testing.T) {
	bm := NewBreakpointManager(nil)
	bm.timeout = 40 * time.Millisecond
	f := newReqFlow()
	f.State = flow.StateAwaitingResponse

	start := time.Now()
	abort := bm.Pause(f, flow.PhaseRequest)
	elapsed := time.Since(start)

	if abort {
		t.Error("超时应失败开放,而不是阻断")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("提前返回: elapsed = %v, want >= 40ms", elapsed)
	}
	if f.State != flow.StateAwaitingResponse {
		t.Errorf("状态 = %q, want %q", f.State, flow.StateAwaitingResponse)
	}
	if f.Metadata["breakpointTimedOut"] != true {
		t.Errorf("Metadata[breakpointTimedOut] = %v, want true", f.Metadata["breakpointTimedOut"])
	}
	if f.Modified {
		t.Error("超时未编辑,不应标记 Modified")
	}
	if n := len(bm.List()); n != 0 {
		t.Errorf("超时后暂停列表应为空, got %d", n)
	}
}

// Metadata 为 nil 的 flow(非 flow.New 构造)在超时路径上不能空 map 写入 panic。
func TestPauseTimeoutInitializesNilMetadata(t *testing.T) {
	bm := NewBreakpointManager(nil)
	bm.timeout = 20 * time.Millisecond
	f := &flow.Flow{ID: flow.NewID(), Protocol: flow.ProtoHTTP, State: flow.StatePending}

	if abort := bm.Pause(f, flow.PhaseRequest); abort {
		t.Error("超时应失败开放")
	}
	if f.Metadata["breakpointTimedOut"] != true {
		t.Errorf("应初始化 Metadata 并写入标记, got %v", f.Metadata)
	}
}

// 超过并发上限时立即失败开放:不挂起、不改状态、不广播事件。
func TestPauseMaxOpenFailsOpen(t *testing.T) {
	sink := &eventSink{}
	bm := NewBreakpointManager(sink.emit)
	bm.maxOpen = 1

	first := newReqFlow()
	done := make(chan bool, 1)
	go func() { done <- bm.Pause(first, flow.PhaseRequest) }()
	waitPaused(t, bm, first.ID)
	// 不等 hit 发出的话,下面「被拒绝的 flow 没产生事件」的断言在空 sink 上恒真。
	sink.waitTypes(t, "breakpoint_hit")

	second := newReqFlow()
	start := time.Now()
	abort := bm.Pause(second, flow.PhaseRequest)
	elapsed := time.Since(start)

	if abort {
		t.Error("超上限应失败开放")
	}
	if elapsed > time.Second {
		t.Errorf("超上限应立即返回, elapsed = %v", elapsed)
	}
	if second.State == flow.StatePausedAtBreakpoint {
		t.Error("被拒绝的 flow 不应进入暂停状态")
	}
	if got := sink.types(); got != "breakpoint_hit" {
		t.Errorf("被拒绝的 flow 不应产生事件: 事件序列 = %q, want %q", got, "breakpoint_hit")
	}

	if !bm.Resume(first.ID, nil) {
		t.Fatal("Resume 应成功")
	}
	<-done
}

// ---- 事件广播 ----

// 挂起/放行各广播一次,载荷须是快照:消费者异步序列化,而放行后处理器会就地改写。
func TestPauseEmitsHitAndResolvedSnapshots(t *testing.T) {
	sink := &eventSink{}
	bm := NewBreakpointManager(sink.emit)
	f := newReqFlow()

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseRequest) }()
	waitPaused(t, bm, f.ID)
	if !bm.Resume(f.ID, nil) {
		t.Fatal("Resume 应成功")
	}
	<-done

	events := sink.snapshot()
	if got, want := sink.types(), "breakpoint_hit,breakpoint_resolved"; got != want {
		t.Fatalf("事件序列 = %q, want %q", got, want)
	}

	hit, ok := events[0].payload.(*flow.Flow)
	if !ok {
		t.Fatalf("hit 载荷类型 = %T, want *flow.Flow", events[0].payload)
	}
	if hit == f {
		t.Error("载荷应是快照,不能是活指针")
	}
	if hit.State != flow.StatePausedAtBreakpoint {
		t.Errorf("hit 载荷状态 = %q, want %q", hit.State, flow.StatePausedAtBreakpoint)
	}

	resolved, ok := events[1].payload.(*flow.Flow)
	if !ok {
		t.Fatalf("resolved 载荷类型 = %T, want *flow.Flow", events[1].payload)
	}
	if resolved == f {
		t.Error("resolved 载荷同样应是快照,不能是活指针")
	}
	if resolved.State != flow.StatePending {
		t.Errorf("resolved 载荷状态 = %q, want %q(已恢复)", resolved.State, flow.StatePending)
	}
}

// emit 由装配层注入,它 panic 时条目仍须摘除,否则会永久占住一个 maxOpen 名额。
func TestPauseCleansUpWhenEmitPanics(t *testing.T) {
	bm := NewBreakpointManager(func(typ string, _ any) {
		if typ == evtBreakpointHit {
			panic("注入的 emitter panic")
		}
	})

	func() {
		defer func() {
			if recover() == nil {
				t.Error("emitter 的 panic 应向上传播,而不是被 Pause 吞掉")
			}
		}()
		bm.Pause(newReqFlow(), flow.PhaseRequest)
	}()

	if n := len(bm.List()); n != 0 {
		t.Errorf("emit panic 后暂停列表应已清空, got %d(maxOpen 名额泄漏)", n)
	}
}

// ---- deliver 的边界 ----

func TestResumeAndAbortUnknownID(t *testing.T) {
	bm := NewBreakpointManager(nil)
	if bm.Resume("不存在", nil) {
		t.Error("Resume 未知 ID 应返回 false")
	}
	if bm.Abort("不存在") {
		t.Error("Abort 未知 ID 应返回 false")
	}
}

// resume 通道容量为 1:重复放行(UI 连点)不能阻塞调用方,只返回 false。
func TestDeliverDoesNotBlockWhenBufferFull(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newReqFlow()
	bm.paused[f.ID] = &paused{flow: f, phase: flow.PhaseRequest, resume: make(chan resumeMsg, 1)}

	if !bm.Resume(f.ID, nil) {
		t.Fatal("首次 Resume 应成功")
	}

	done := make(chan bool, 1)
	go func() { done <- bm.Resume(f.ID, nil) }()
	select {
	case ok := <-done:
		if ok {
			t.Error("缓冲已满时应返回 false")
		}
	case <-time.After(time.Second):
		t.Fatal("重复 Resume 阻塞了调用方")
	}
}

// ---- List ----

// List 返回快照,调用方改动它不能影响仍在暂停中的原 flow。
func TestListReturnsSnapshots(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newReqFlow()

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseRequest) }()
	waitPaused(t, bm, f.ID)

	listed := bm.List()
	if len(listed) != 1 {
		t.Fatalf("暂停列表长度 = %d, want 1", len(listed))
	}
	if listed[0] == f {
		t.Error("List 应返回快照,不能是活指针")
	}
	listed[0].Request.URL = "https://tampered/"

	if f.Request.URL != "https://x.com/" {
		t.Errorf("原 flow 被快照改动污染: URL = %q", f.Request.URL)
	}

	if !bm.Resume(f.ID, nil) {
		t.Fatal("Resume 应成功")
	}
	<-done
}

func TestListEmptyWhenNothingPaused(t *testing.T) {
	if n := len(NewBreakpointManager(nil).List()); n != 0 {
		t.Errorf("初始暂停列表长度 = %d, want 0", n)
	}
}

// ---- 全局开关 ----

func TestGlobalBreakRoundTrip(t *testing.T) {
	bm := NewBreakpointManager(nil)
	if req, resp := bm.GlobalBreak(); req || resp {
		t.Errorf("初始开关 = %v/%v, want false/false", req, resp)
	}

	bm.SetGlobalBreak(true, false)
	if req, resp := bm.GlobalBreak(); !req || resp {
		t.Errorf("开关 = %v/%v, want true/false", req, resp)
	}
	if !bm.ShouldBreak(flow.PhaseRequest) {
		t.Error("请求阶段应命中全局开关")
	}
	if bm.ShouldBreak(flow.PhaseResponse) {
		t.Error("响应阶段不应命中")
	}
}

// ShouldBreak 只看全局开关,URL 规则不参与(与 ShouldBreakFor 的分工)。
func TestShouldBreakIgnoresURLRules(t *testing.T) {
	bm := NewBreakpointManager(nil)
	bm.AddRule("https://x.com/*", true, true)

	if bm.ShouldBreak(flow.PhaseRequest) {
		t.Error("ShouldBreak 不应考虑 URL 规则")
	}
	if !bm.ShouldBreakFor("https://x.com/a", flow.PhaseRequest) {
		t.Error("ShouldBreakFor 应命中 URL 规则")
	}
}

// 未知阶段下两条路径不一致:全局开关按阶段精确匹配故不命中,而 URL 规则的阶段过滤
// 只对 request/response 生效。钉住当前行为 —— 新增阶段时须一并改 ShouldBreakFor。
func TestUnknownPhaseGlobalOffButRuleStillMatches(t *testing.T) {
	bm := NewBreakpointManager(nil)
	bm.SetGlobalBreak(true, true)
	bm.AddRule("https://x.com/*", true, true)

	if bm.ShouldBreak(flow.Phase("connect")) {
		t.Error("未知阶段不应命中全局开关")
	}
	if !bm.ShouldBreakFor("https://x.com/a", flow.Phase("connect")) {
		t.Error("规则未按未知阶段过滤:当前实现只在 request/response 上做阶段判断")
	}
}

// ---- URL 规则 CRUD ----

func TestShouldBreakForURLRule(t *testing.T) {
	bm := NewBreakpointManager(nil)
	r := bm.AddRule("https://api.x.com/*", true, false)
	if r.ID == "" {
		t.Fatal("AddRule 应返回带 ID 的规则")
	}
	if !bm.ShouldBreakFor("https://api.x.com/v1", flow.PhaseRequest) {
		t.Error("请求阶段规则应在匹配 URL 上命中")
	}
	if bm.ShouldBreakFor("https://api.x.com/v1", flow.PhaseResponse) {
		t.Error("规则只覆盖请求阶段,响应不应命中")
	}
	if bm.ShouldBreakFor("https://other.com/v1", flow.PhaseRequest) {
		t.Error("不匹配的 URL 不应命中")
	}

	bm.ToggleRule(r.ID, false)
	if bm.ShouldBreakFor("https://api.x.com/v1", flow.PhaseRequest) {
		t.Error("禁用的规则不应命中")
	}

	bm.DeleteRule(r.ID)
	if len(bm.ListRules()) != 0 {
		t.Error("规则应已删除")
	}

	bm.SetGlobalBreak(false, true)
	if !bm.ShouldBreakFor("https://whatever", flow.PhaseResponse) {
		t.Error("全局响应断点应作用于任意 URL")
	}
}

// 多条规则中只要有一条启用且匹配就命中。
func TestShouldBreakForAnyMatchingRule(t *testing.T) {
	bm := NewBreakpointManager(nil)
	disabled := bm.AddRule("https://a.com/*", true, true)
	bm.ToggleRule(disabled.ID, false)
	bm.AddRule("https://b.com/*", true, true) // 启用但不匹配
	bm.AddRule("https://c.com/*", true, true) // 启用且匹配

	if !bm.ShouldBreakFor("https://c.com/x", flow.PhaseRequest) {
		t.Error("应命中第三条规则")
	}
	if bm.ShouldBreakFor("https://a.com/x", flow.PhaseRequest) {
		t.Error("被禁用的规则不应命中")
	}
	if bm.ShouldBreakFor("https://d.com/x", flow.PhaseRequest) {
		t.Error("无规则匹配时不应命中")
	}
}

// 规则的阶段开关必须双向生效:只勾请求的规则不能在响应阶段命中,反之亦然。
func TestRulePhaseGating(t *testing.T) {
	cases := []struct {
		name          string
		onReq, onResp bool
		phase         flow.Phase
		want          bool
	}{
		{"只勾请求/请求阶段", true, false, flow.PhaseRequest, true},
		{"只勾请求/响应阶段", true, false, flow.PhaseResponse, false},
		{"只勾响应/请求阶段", false, true, flow.PhaseRequest, false},
		{"只勾响应/响应阶段", false, true, flow.PhaseResponse, true},
		{"都勾/请求阶段", true, true, flow.PhaseRequest, true},
		{"都勾/响应阶段", true, true, flow.PhaseResponse, true},
		{"都不勾/请求阶段", false, false, flow.PhaseRequest, false},
		{"都不勾/响应阶段", false, false, flow.PhaseResponse, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bm := NewBreakpointManager(nil)
			bm.AddRule("https://x.com/*", c.onReq, c.onResp)
			if got := bm.ShouldBreakFor("https://x.com/a", c.phase); got != c.want {
				t.Errorf("ShouldBreakFor = %v, want %v", got, c.want)
			}
		})
	}
}

// AddRule / ListRules 都返回副本,外部改动不能穿透到管理器内部状态。
func TestRuleAccessorsReturnCopies(t *testing.T) {
	bm := NewBreakpointManager(nil)
	added := bm.AddRule("https://x.com/*", true, false)
	added.URL = "https://tampered/"
	added.Enabled = false

	listed := bm.ListRules()
	if len(listed) != 1 {
		t.Fatalf("规则数 = %d, want 1", len(listed))
	}
	if listed[0].URL != "https://x.com/*" || !listed[0].Enabled {
		t.Errorf("AddRule 返回值被改动后影响了内部状态: %+v", listed[0])
	}

	listed[0].URL = "https://tampered-again/"
	if again := bm.ListRules(); again[0].URL != "https://x.com/*" {
		t.Errorf("ListRules 返回值被改动后影响了内部状态: %q", again[0].URL)
	}
}

func TestAddRuleGeneratesDistinctIDs(t *testing.T) {
	bm := NewBreakpointManager(nil)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := bm.AddRule("https://x.com/*", true, true).ID
		if !strings.HasPrefix(id, "bp-") {
			t.Fatalf("规则 ID 应以 bp- 开头, got %q", id)
		}
		if seen[id] {
			t.Fatalf("规则 ID 重复: %q", id)
		}
		seen[id] = true
	}
}

func TestUpdateRule(t *testing.T) {
	bm := NewBreakpointManager(nil)
	r := bm.AddRule("https://old.com/*", true, true)

	if !bm.UpdateRule(r.ID, "https://new.com/*", false, true, false) {
		t.Fatal("UpdateRule 应返回 true")
	}
	got := bm.ListRules()[0]
	if got.URL != "https://new.com/*" || got.OnRequest || !got.OnResponse || got.Enabled {
		t.Errorf("更新后规则 = %+v", got)
	}

	// 空 URL 表示不改 URL,但其余字段照常覆盖。
	if !bm.UpdateRule(r.ID, "", true, false, true) {
		t.Fatal("UpdateRule 应返回 true")
	}
	got = bm.ListRules()[0]
	if got.URL != "https://new.com/*" {
		t.Errorf("空 URL 不应改动 URL, got %q", got.URL)
	}
	if !got.OnRequest || got.OnResponse || !got.Enabled {
		t.Errorf("其余字段应被覆盖: %+v", got)
	}

	if bm.UpdateRule("不存在", "https://x/", true, true, true) {
		t.Error("更新未知 ID 应返回 false")
	}
}

func TestToggleRuleUnknownID(t *testing.T) {
	bm := NewBreakpointManager(nil)
	if got, ok := bm.ToggleRule("不存在", true); ok || got != nil {
		t.Errorf("切换未知 ID = (%+v, %v), want (nil, false)", got, ok)
	}
}

func TestToggleRuleReturnsUpdatedCopy(t *testing.T) {
	bm := NewBreakpointManager(nil)
	created := bm.AddRule("https://x.com/*", true, false)

	got, ok := bm.ToggleRule(created.ID, false)
	if !ok || got == nil || got.ID != created.ID || got.Enabled {
		t.Fatalf("ToggleRule = (%+v, %v), want disabled rule", got, ok)
	}

	got.Enabled = true
	if stored := bm.ListRules()[0]; stored.Enabled {
		t.Errorf("修改 ToggleRule 返回值后影响了内部状态: %+v", stored)
	}
}

func TestDeleteRulePreservesOrder(t *testing.T) {
	bm := NewBreakpointManager(nil)
	a := bm.AddRule("https://a.com/*", true, true)
	b := bm.AddRule("https://b.com/*", true, true)
	c := bm.AddRule("https://c.com/*", true, true)

	bm.DeleteRule(b.ID)

	rules := bm.ListRules()
	if len(rules) != 2 || rules[0].ID != a.ID || rules[1].ID != c.ID {
		t.Errorf("删除中间项后顺序错误: %+v", rules)
	}

	bm.DeleteRule("不存在") // 不应影响现有规则
	if len(bm.ListRules()) != 2 {
		t.Error("删除未知 ID 不应改动规则集")
	}
}

// ---- URL 通配匹配 ----

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		name         string
		pattern, url string
		want         bool
	}{
		{"尾部通配命中", "https://api.sniffy.dev/v1/*", "https://api.sniffy.dev/v1/orders", true},
		{"尾部通配不命中", "https://api.sniffy.dev/v1/*", "https://api.sniffy.dev/v2/orders", false},
		{"中间通配命中", "https://*.example.com/checkout", "https://shop.example.com/checkout", true},
		{"中间通配需至少匹配分隔符前缀", "https://*.example.com/checkout", "https://example.com/checkout", false},
		{"无通配退化为子串包含", "analytics", "https://analytics.google.com/x", true},
		{"无通配子串不命中", "analytics", "https://google.com/x", false},
		{"空模式不匹配", "", "https://anything", false},
		{"纯空白模式不匹配", "   ", "https://anything", false},
		{"单星匹配一切", "*", "https://anything", true},
		{"单星匹配空串", "*", "", true},
		{"扩展名通配", "https://a.com/*.json", "https://a.com/data/x.json", true},
		{"扩展名通配不命中", "https://a.com/*.json", "https://a.com/data/x.txt", false},
		{"多段通配", "https://*.a.com/*/detail", "https://x.a.com/y/detail", true},
		{"含通配时锚定开头", "a.com/*", "https://a.com/x", false},
		{"含通配时锚定结尾", "https://a.com/*.json", "https://a.com/x.json?v=1", false},
		{"模式两端空白被裁剪", "  analytics  ", "https://analytics.com", true},
		{"查询串中的问号按字面量处理", "https://a.com/s?q=*", "https://a.com/s?q=1", true},
		{"问号不作为正则单字符通配", "https://a.com/s?q=*", "https://a.comXs?q=1", false},
		{"点号按字面量处理", "https://a.com/*", "https://aXcom/z", false},
		{"加号按字面量处理", "https://a.com/a+b*", "https://a.com/a+bc", true},
		{"括号按字面量处理", "https://a.com/(x)*", "https://a.com/(x)y", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &BreakRule{URL: c.pattern}
			if got := r.matchesLocked(c.url); got != c.want {
				t.Errorf("规则 %q 匹配 %q = %v, want %v", c.pattern, c.url, got, c.want)
			}
		})
	}
}

// 编译缓存:同一模式只编译一次,模式变了必须重编译 —— 否则规则改了 URL 却仍按旧模式命中。
func TestWildcardCompileCache(t *testing.T) {
	r := &BreakRule{URL: "https://a.com/*"}

	if !r.matchesLocked("https://a.com/x") {
		t.Fatal("原模式应命中")
	}
	first := r.re
	if first == nil {
		t.Fatal("含 * 的模式应缓存编译结果")
	}
	if !r.matchesLocked("https://a.com/y") {
		t.Fatal("原模式应命中")
	}
	if r.re != first {
		t.Error("模式未变时不应重新编译")
	}

	r.URL = "https://b.com/*"
	if r.matchesLocked("https://a.com/x") {
		t.Error("改了 URL 后旧模式仍命中:缓存未失效")
	}
	if !r.matchesLocked("https://b.com/x") {
		t.Error("改了 URL 后新模式应命中")
	}
	if r.re == first {
		t.Error("模式变化后应重新编译")
	}

	// 不含 * 的模式走子串匹配,不应留下正则缓存。
	plain := &BreakRule{URL: "analytics"}
	if !plain.matchesLocked("https://analytics.com/x") || plain.re != nil {
		t.Errorf("子串模式不应编译正则: re=%v", plain.re)
	}
}

// 经 UpdateRule 改 URL 后匹配结果必须跟着变(端到端验证缓存失效)。
func TestUpdateRuleInvalidatesWildcardCache(t *testing.T) {
	bm := NewBreakpointManager(nil)
	r := bm.AddRule("https://a.com/*", true, true)
	if !bm.ShouldBreakFor("https://a.com/x", flow.PhaseRequest) {
		t.Fatal("原模式应命中")
	}

	if !bm.UpdateRule(r.ID, "https://b.com/*", true, true, true) {
		t.Fatal("UpdateRule 应成功")
	}
	if bm.ShouldBreakFor("https://a.com/x", flow.PhaseRequest) {
		t.Error("改 URL 后旧模式仍命中:编译缓存未失效")
	}
	if !bm.ShouldBreakFor("https://b.com/x", flow.PhaseRequest) {
		t.Error("改 URL 后新模式应命中")
	}
}

// ---- 并发 ----

// Pause(处理器)、Resume/List/规则 CRUD(UI)、ShouldBreakFor(热路径)三方并发访问。
func TestBreakpointManagerConcurrentAccess(t *testing.T) {
	bm := NewBreakpointManager(func(string, any) {})
	bm.timeout = 3 * time.Second

	const pausers = 16
	flows := make([]*flow.Flow, pausers)
	for i := range flows {
		flows[i] = newReqFlow()
	}

	var wg sync.WaitGroup
	aborted := make(chan bool, pausers)
	for _, f := range flows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			aborted <- bm.Pause(f, flow.PhaseRequest)
		}()
	}

	stop := make(chan struct{})
	wg.Add(1)
	go func() { // UI 侧:轮询暂停列表并放行
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, f := range bm.List() {
				// 一律带编辑放行:mergeFlow 与 Modified 的写入是「摘除后才改写 f」的另一半,
				// 需与 List() 并发跑才抓得到回归。f 本身就是快照,直接改当作 UI 编辑结果。
				f.Request.URL = editedURL
				bm.Resume(f.ID, f)
			}
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() { // 独立只读侧(REST 轮询 / 桌面刷新)。须与放行分属不同 goroutine:
		// 同一 goroutine 里 List() 与它自己触发的 mergeFlow 天然串行,窗口不重叠。
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			bm.List()
		}
	}()

	wg.Add(1)
	go func() { // 规则 CRUD 与热路径查询并发进行
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			r := bm.AddRule("https://x.com/*", true, true)
			bm.ShouldBreakFor("https://x.com/a", flow.PhaseRequest)
			bm.ListRules()
			bm.ToggleRule(r.ID, false)
			bm.DeleteRule(r.ID)
		}
	}()

	for i := 0; i < pausers; i++ {
		select {
		case abort := <-aborted:
			if abort {
				t.Error("Resume 放行不应返回阻断")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("等待 Pause 返回超时:可能死锁")
		}
	}
	close(stop)
	wg.Wait()

	if n := len(bm.List()); n != 0 {
		t.Errorf("全部放行后暂停列表应为空, got %d", n)
	}
	// 没有这两条断言,deliver 整体失效时本用例会静默退化成「全部走超时兜底」而依旧 PASS。
	for i, f := range flows {
		if f.Metadata["breakpointTimedOut"] == true {
			t.Errorf("flow %d 落到了超时兜底,说明 Resume 并未真正放行", i)
		}
		if !f.Modified || f.Request.URL != editedURL {
			t.Errorf("flow %d 的编辑未合并: Modified=%v URL=%q", i, f.Modified, f.Request.URL)
		}
	}
}
