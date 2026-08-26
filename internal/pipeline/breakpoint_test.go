// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// editedURL 是各用例模拟「UI 编辑后放行」时写入的请求 URL。
var editedURL = "https://edited.example/"

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

// 带编辑放行:只改编辑里点名的字段,flow 的其余部分与暂停前状态原样保留。
// edit 刻意走一遍 JSON 往返 —— 生产里它就是从前端 JSON 解出来的,直接用进程内构造的
// 结构体会绕开「缺省字段 = 没动过」这条最容易写错的语义。
func TestPauseResumeAppliesEditFieldsOnly(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newRespFlow()
	f.State = flow.StateAwaitingResponse
	f.Tags = []string{"原始标签"}
	f.Response.Header = map[string][]string{"Content-Type": {"text/plain"}}

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseResponse) }()
	waitPaused(t, bm, f.ID)

	// 请求编辑刻意一并送上:响应阶段的请求早已发出,后端必须原样忽略它。
	edit := decodeEdit(t, `{"request":{"url":"https://edited.example/x"},"response":{"status":418}}`)
	if err := bm.Resume(f.ID, edit); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
	}

	select {
	case abort := <-done:
		if abort {
			t.Fatal("Resume 不应返回阻断")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Pause 未在超时前返回")
	}

	if f.Request.URL != "https://x.com/" {
		t.Errorf("响应阶段不应改动请求: URL = %q", f.Request.URL)
	}
	if f.Response.Status != 418 {
		t.Errorf("状态码未应用: %d", f.Response.Status)
	}
	// 只给了状态码没给文本:必须重新派生,否则出线拼成 "HTTP/1.1 418 200 OK"。
	if f.Response.StatusText != "I'm a teapot" {
		t.Errorf("状态文本未跟随状态码: %q", f.Response.StatusText)
	}
	// 编辑里没提的字段一律不动。
	if got := f.Response.Header["Content-Type"]; len(got) != 1 || got[0] != "text/plain" {
		t.Errorf("编辑未提及响应头,不应被清空: %v", f.Response.Header)
	}
	if string(f.Response.Body) != "ok" {
		t.Errorf("编辑未提及 body,不应被清空: %q", f.Response.Body)
	}
	if !f.Modified {
		t.Error("带编辑放行应标记 Modified")
	}
	if got := strings.Join(f.Tags, ","); got != "原始标签" {
		t.Errorf("Tags 不在编辑范围内, got %q", got)
	}
	if f.State != flow.StateAwaitingResponse {
		t.Errorf("状态 = %q, want %q(恢复暂停前的状态)", f.State, flow.StateAwaitingResponse)
	}
	if n := len(bm.List()); n != 0 {
		t.Errorf("放行后暂停列表应为空, got %d", n)
	}
}

// 改 URL 必须连带改 Host 与 Path,否则请求会带着旧主机名出线;而用户手改过的 Host 行
// 优先于 URL 派生 —— 改 Host 头是常见的定向测试手法,不能被 URL 悄悄覆盖。
func TestRequestEditDerivesHostFromURL(t *testing.T) {
	for name, tc := range map[string]struct {
		payload  string
		wantHost string
		wantPath string
	}{
		"只改 URL": {
			payload:  `{"request":{"url":"https://edited.example/x"}}`,
			wantHost: "edited.example",
			wantPath: "/x",
		},
		"编辑器回传的 Host 行仍是旧值时由 URL 接管": {
			payload:  `{"request":{"url":"https://edited.example/x","headers":[["Host","x.com"],["Accept","*/*"]]}}`,
			wantHost: "edited.example",
			wantPath: "/x",
		},
		"手改过的 Host 行优先": {
			payload:  `{"request":{"url":"https://edited.example/x","headers":[["Host","pinned.internal"]]}}`,
			wantHost: "pinned.internal",
			wantPath: "/x",
		},
	} {
		t.Run(name, func(t *testing.T) {
			bm := NewBreakpointManager(nil)
			f := newReqFlow()
			done := make(chan bool, 1)
			go func() { done <- bm.Pause(f, flow.PhaseRequest) }()
			waitPaused(t, bm, f.ID)

			if err := bm.Resume(f.ID, decodeEdit(t, tc.payload)); err != nil {
				t.Fatalf("Resume 应成功: %v", err)
			}
			<-done

			if f.Request.URL != "https://edited.example/x" {
				t.Errorf("URL = %q", f.Request.URL)
			}
			if f.Request.Host != tc.wantHost || f.Request.Path != tc.wantPath {
				t.Errorf("host=%q path=%q, want host=%q path=%q", f.Request.Host, f.Request.Path, tc.wantHost, tc.wantPath)
			}
		})
	}
}

// 断点放行绝不能替换 Request/Response 指针:它们身上挂着不进 JSON 的原始线缆形态。
// 私有字段丢失里最要命的一条是 truncated —— 上游截断的响应会被重算成一份长度自洽、
// 客户端察觉不到的"完整"响应。
func TestPauseResumeKeepsOffWireState(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newRespFlow()
	req, resp := f.Request, f.Response
	req.SetOriginalBody([]byte("gzipped"), req.Body, "gzip")
	resp.SetOriginalHead("HTTP/1.1 200 OK")
	resp.SetOriginalBody([]byte("gzipped"), resp.Body, "gzip")
	resp.SetPassthroughBody("/tmp/cache/body", 4096)
	resp.MarkTruncated()
	resp.Trailer = map[string][]string{"Grpc-Status": {"0"}}

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseResponse) }()
	waitPaused(t, bm, f.ID)

	if err := bm.Resume(f.ID, decodeEdit(t, `{"response":{"status":503}}`)); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
	}
	<-done

	if f.Request != req || f.Response != resp {
		t.Fatal("放行不得替换 Request/Response 指针,否则非导出的线缆状态全部清零")
	}
	// body 没被改过,原始编码字节仍应原样回放。
	if got := resp.OriginalEncodedBody(resp.Body); string(got) != "gzipped" {
		t.Errorf("响应保真回放丢失: %q", got)
	}
	if got := req.OriginalEncodedBody(req.Body); string(got) != "gzipped" {
		t.Errorf("请求保真回放丢失: %q", got)
	}
	if path, size := resp.BodyFile(); path != "/tmp/cache/body" || size != 4096 {
		t.Errorf("透传旁路的落盘副本丢失: %q %d", path, size)
	}
	if len(resp.Trailer) != 1 {
		t.Errorf("编辑未提及 Trailer,不应被清空: %v", resp.Trailer)
	}
}

// 头部编辑是整体重设:线上顺序与大小写按用户排好的写,Host 行单独拎进 Request.Host
// (出站从 Request.Host 取 Host,留在 map 里会被静默忽略)。
func TestPauseResumeRewritesHeadersInOrder(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Accept": {"*/*"}, "X-Old": {"1"}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"accept", "*/*"}, {"X-Old", "1"}}

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseRequest) }()
	waitPaused(t, bm, f.ID)

	edit := decodeEdit(t, `{"request":{"headers":[["Host","proxy.internal"],["x-new","2"],["accept","application/json"]]}}`)
	if err := bm.Resume(f.ID, edit); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
	}
	<-done

	if f.Request.Host != "proxy.internal" {
		t.Errorf("Host 头未落到 Request.Host: %q", f.Request.Host)
	}
	if _, ok := f.Request.Header["Host"]; ok {
		t.Error("Host 不应留在 Header map 里")
	}
	if got := f.Request.Header["X-New"]; len(got) != 1 || got[0] != "2" {
		t.Errorf("新增头未规范化进 map: %v", f.Request.Header)
	}
	if _, ok := f.Request.Header["X-Old"]; ok {
		t.Error("编辑里没有的头应被删除")
	}
	want := [][2]string{{"Host", "proxy.internal"}, {"x-new", "2"}, {"accept", "application/json"}}
	if !sameHeaderPairs(f.Request.RawHeaders, want) {
		t.Errorf("线上头序列 = %v, want %v", f.Request.RawHeaders, want)
	}
}

// 编辑不合法时必须原地拒绝:flow 继续按在断点上,用户改回来还能再放行一次。
func TestResumeRejectsInvalidEditAndKeepsFlowPaused(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newReqFlow()

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseRequest) }()
	waitPaused(t, bm, f.ID)

	for name, payload := range map[string]string{
		"头部含 CRLF": `{"request":{"headers":[["X-Evil","a\r\nX-Injected: 1"]]}}`,
		"方法含空格":    `{"request":{"method":"GET /admin HTTP/1.1"}}`,
		"URL 无主机名": `{"request":{"url":"/relative"}}`,
		"状态码越界":    `{"response":{"status":999}}`,
	} {
		if err := bm.Resume(f.ID, decodeEdit(t, payload)); err == nil {
			t.Errorf("%s: 应被拒绝", name)
		} else if errors.Is(err, ErrBreakpointNotFound) {
			t.Errorf("%s: 应是校验错误而不是「已解除」", name)
		}
	}

	if n := len(bm.List()); n != 1 {
		t.Fatalf("被拒绝的编辑不应放行 flow, 暂停列表 = %d", n)
	}
	if err := bm.Resume(f.ID, nil); err != nil {
		t.Fatalf("改回来后应能正常放行: %v", err)
	}
	<-done
}

// decodeEdit 按生产里的形状(前端 JSON)构造一份编辑。
func decodeEdit(t *testing.T, payload string) *BreakpointEdit {
	t.Helper()
	var edit *BreakpointEdit
	if err := json.Unmarshal([]byte(payload), &edit); err != nil {
		t.Fatalf("用例载荷不是合法 JSON: %v", err)
	}
	return edit
}

// 不带编辑放行:flow 内容与 Modified 标记都不应被动到。
func TestPauseResumeWithoutEditsKeepsFlowIntact(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newReqFlow()

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseRequest) }()
	waitPaused(t, bm, f.ID)

	if err := bm.Resume(f.ID, nil); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
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

	if err := bm.Abort(f.ID); err != nil {
		t.Fatalf("Abort 应成功: %v", err)
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

	if err := bm.Resume(first.ID, nil); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
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
	if err := bm.Resume(f.ID, nil); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
	}
	<-done

	events := sink.snapshot()
	if got, want := sink.types(), "breakpoint_hit,breakpoint_resolved"; got != want {
		t.Fatalf("事件序列 = %q, want %q", got, want)
	}

	hit, ok := events[0].payload.(*BreakpointFlow)
	if !ok {
		t.Fatalf("hit 载荷类型 = %T, want *BreakpointFlow", events[0].payload)
	}
	if hit.Flow == f {
		t.Error("载荷应是快照,不能是活指针")
	}
	if hit.State != flow.StatePausedAtBreakpoint {
		t.Errorf("hit 载荷状态 = %q, want %q", hit.State, flow.StatePausedAtBreakpoint)
	}
	if hit.PausedUntil.IsZero() {
		t.Error("hit 载荷应带截止时刻,否则 UI 无从显示倒计时")
	}

	resolved, ok := events[1].payload.(*BreakpointFlow)
	if !ok {
		t.Fatalf("resolved 载荷类型 = %T, want *BreakpointFlow", events[1].payload)
	}
	if resolved.Flow == f {
		t.Error("resolved 载荷同样应是快照,不能是活指针")
	}
	if resolved.State != flow.StatePending {
		t.Errorf("resolved 载荷状态 = %q, want %q(已恢复)", resolved.State, flow.StatePending)
	}
	if resolved.Resolution != ResolutionResumed {
		t.Errorf("resolved 载荷 Resolution = %q, want %q", resolved.Resolution, ResolutionResumed)
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
	if !errors.Is(bm.Resume("不存在", nil), ErrBreakpointNotFound) {
		t.Error("Resume 未知 ID 应返回 ErrBreakpointNotFound")
	}
	if !errors.Is(bm.Abort("不存在"), ErrBreakpointNotFound) {
		t.Error("Abort 未知 ID 应返回 ErrBreakpointNotFound")
	}
}

// resume 通道容量为 1:重复放行(UI 连点)不能阻塞调用方,只返回 ErrBreakpointNotFound。
func TestDeliverDoesNotBlockWhenBufferFull(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newReqFlow()
	bm.paused[f.ID] = &paused{flow: f, phase: flow.PhaseRequest, resume: make(chan resumeMsg, 1)}

	if err := bm.Resume(f.ID, nil); err != nil {
		t.Fatalf("首次 Resume 应成功: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- bm.Resume(f.ID, nil) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBreakpointNotFound) {
			t.Errorf("缓冲已满时应返回 ErrBreakpointNotFound, got %v", err)
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
	if listed[0].Flow == f {
		t.Error("List 应返回快照,不能是活指针")
	}
	listed[0].Request.URL = "https://tampered/"

	if f.Request.URL != "https://x.com/" {
		t.Errorf("原 flow 被快照改动污染: URL = %q", f.Request.URL)
	}

	if err := bm.Resume(f.ID, nil); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
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
			for _, item := range bm.List() {
				// 一律带编辑放行:mergeFlow 与 Modified 的写入是「摘除后才改写 f」的另一半,
				// 需与 List() 并发跑才抓得到回归。载荷本身就是快照,直接改当作 UI 编辑结果。
				edit := &BreakpointEdit{Request: &RequestEdit{URL: &editedURL}}
				_ = bm.Resume(item.ID, edit)
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

// ---- 规则持久化 ----

// 规则 CRUD 必须写时落盘,且回调拿到的是一份不含正则缓存的快照 ——
// 缓存字段带着走会被落盘侧序列化不到、又在装配层转换时丢失,徒增两处不一致。
func TestRuleMutationsFlushSnapshot(t *testing.T) {
	bm := NewBreakpointManager(nil)
	var mu sync.Mutex
	var flushes [][]*BreakRule
	bm.SetPersist(func(rs []*BreakRule) error {
		mu.Lock()
		defer mu.Unlock()
		flushes = append(flushes, rs)
		return nil
	})

	r := bm.AddRule("https://a.example/*", true, false)
	if _, ok := bm.UpdateRuleFields(r.ID, "https://b.example/*", true, true, nil); !ok {
		t.Fatal("UpdateRuleFields 应命中")
	}
	if _, ok := bm.ToggleRule(r.ID, false); !ok {
		t.Fatal("ToggleRule 应命中")
	}
	if !bm.DeleteRule(r.ID) {
		t.Fatal("DeleteRule 应命中")
	}
	// 未命中的写入口不该产生落盘。
	bm.UpdateRule("不存在", "x", true, true, true)
	bm.ToggleRule("不存在", true)
	bm.DeleteRule("不存在")

	mu.Lock()
	defer mu.Unlock()
	if len(flushes) != 4 {
		t.Fatalf("落盘次数 = %d, want 4(增/改/启停/删各一次)", len(flushes))
	}
	if len(flushes[1]) != 1 || flushes[1][0].URL != "https://b.example/*" {
		t.Errorf("改后的快照 = %+v", flushes[1])
	}
	if flushes[2][0].Enabled {
		t.Errorf("启停后的快照未反映 Enabled: %+v", flushes[2])
	}
	if len(flushes[3]) != 0 {
		t.Errorf("删除后的快照应为空, got %d", len(flushes[3]))
	}
}

// RestoreRules 整体替换规则集合,且不触发落盘(刚从盘上读回来的东西没必要再写一遍)。
func TestRestoreRulesReplacesWithoutFlush(t *testing.T) {
	bm := NewBreakpointManager(nil)
	flushed := false
	bm.SetPersist(func([]*BreakRule) error { flushed = true; return nil })
	bm.AddRule("https://old.example/*", true, false)
	flushed = false

	bm.RestoreRules([]*BreakRule{
		nil, // 装配层转换出的空洞不应进入集合
		{ID: "bp-1", URL: "https://kept.example/*", OnRequest: true, Enabled: true},
	})
	if flushed {
		t.Error("RestoreRules 不应触发落盘")
	}
	got := bm.ListRules()
	if len(got) != 1 || got[0].URL != "https://kept.example/*" {
		t.Fatalf("恢复后的规则 = %+v", got)
	}
	// 恢复的规则须真的参与匹配,而不只是躺在列表里。
	if !bm.ShouldBreakFor("https://kept.example/a", flow.PhaseRequest) {
		t.Error("恢复的规则应参与热路径匹配")
	}
}

// 落盘失败不能推进版本号:推进了的话,后来那次版本更旧却内容更全的快照会被当成过期丢掉,
// 磁盘停在更早的状态,两次改动一起消失。
func TestFlushRetriesAfterFailure(t *testing.T) {
	bm := NewBreakpointManager(nil)
	var mu sync.Mutex
	var written [][]*BreakRule
	fail := true
	bm.SetPersist(func(rs []*BreakRule) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return errors.New("磁盘满")
		}
		written = append(written, rs)
		return nil
	})

	r := bm.AddRule("https://a.example/*", true, false) // 这次写盘失败
	mu.Lock()
	fail = false
	mu.Unlock()
	if _, ok := bm.ToggleRule(r.ID, false); !ok {
		t.Fatal("ToggleRule 应命中")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(written) != 1 {
		t.Fatalf("失败之后的那次改动应重新落盘, 实际落盘 %d 次", len(written))
	}
	if len(written[0]) != 1 || written[0][0].Enabled {
		t.Errorf("落盘内容应是最新状态: %+v", written[0])
	}
}

// 放行与超时同时到达时,已经投递进来的处置必须认账 —— 否则界面显示"已放行(带修改)",
// 线上发出去的却是未经编辑的原件。
func TestResumeDeliveredAtDeadlineWins(t *testing.T) {
	for i := 0; i < 200; i++ {
		bm := NewBreakpointManager(nil)
		bm.timeout = 2 * time.Millisecond
		f := newReqFlow()

		done := make(chan bool, 1)
		go func() { done <- bm.Pause(f, flow.PhaseRequest) }()

		// 不等 waitPaused:目标就是让投递落在超时那一刻的前后。
		err := bm.Resume(f.ID, decodeEdit(t, `{"request":{"method":"POST"}}`))
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Pause 未返回")
		}

		// 只有两种自洽结局:投递成功 → 编辑必须生效;投递失败 → 编辑必须没生效。
		if err == nil && f.Request.Method != "POST" {
			t.Fatalf("第 %d 轮:Resume 报成功,编辑却被超时吃掉了", i)
		}
		if err != nil && f.Request.Method == "POST" {
			t.Fatalf("第 %d 轮:Resume 报失败,编辑却生效了", i)
		}
	}
}

// 流式 / 透传响应的正文由上游原样中继,把状态码改成无体码会写出一份自相矛盾的报文。
// 这条必须在放行之前挡下并说清楚,而不是让线上出现「204 + 一坨 body」。
func TestResumeRejectsBodylessStatusOnRelayedResponse(t *testing.T) {
	for name, meta := range map[string]string{"流式": "stream", "透传": "passthrough"} {
		t.Run(name, func(t *testing.T) {
			bm := NewBreakpointManager(nil)
			f := newRespFlow()
			f.Metadata[meta] = true

			done := make(chan bool, 1)
			go func() { done <- bm.Pause(f, flow.PhaseResponse) }()
			waitPaused(t, bm, f.ID)

			if err := bm.Resume(f.ID, decodeEdit(t, `{"response":{"status":204}}`)); err == nil {
				t.Error("无体状态码应被拒绝")
			} else if errors.Is(err, ErrBreakpointNotFound) {
				t.Errorf("应是校验错误而不是「已解除」: %v", err)
			}
			if n := len(bm.List()); n != 1 {
				t.Fatalf("被拒绝的编辑不应放行 flow, 暂停列表 = %d", n)
			}

			// 带 body 的状态码照改不误:头与状态行在这两条路上都是生效的。
			if err := bm.Resume(f.ID, decodeEdit(t, `{"response":{"status":503}}`)); err != nil {
				t.Fatalf("普通状态码应能改: %v", err)
			}
			<-done
			if f.Response.Status != 503 {
				t.Errorf("状态码 = %d, want 503", f.Response.Status)
			}
		})
	}
}

// 缓冲路径的正文在内存里,改成无体状态码是合法的(出线侧会同时收掉 body 与长度)。
func TestResumeAllowsBodylessStatusOnBufferedResponse(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newRespFlow()

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseResponse) }()
	waitPaused(t, bm, f.ID)

	if err := bm.Resume(f.ID, decodeEdit(t, `{"response":{"status":304}}`)); err != nil {
		t.Fatalf("缓冲响应改成 304 应被允许: %v", err)
	}
	<-done
	if f.Response.Status != 304 {
		t.Errorf("状态码 = %d, want 304", f.Response.Status)
	}
}

// 换过 body 之后截断标记必须失效,否则出线仍宣告上游那份更长的长度,
// 用户手写的 mock 响应对客户端就是一份读不完的短响应。
func TestResumeBodyEditClearsTruncated(t *testing.T) {
	bm := NewBreakpointManager(nil)
	f := newRespFlow()
	f.Response.Header = map[string][]string{"Content-Length": {"10000"}}
	f.Response.RawHeaders = [][2]string{{"Content-Length", "10000"}}
	f.Response.SetOriginalHead("HTTP/1.1 200 OK")
	f.Response.MarkTruncated()

	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, flow.PhaseResponse) }()
	waitPaused(t, bm, f.ID)

	if err := bm.Resume(f.ID, decodeEdit(t, `{"response":{"body":"{\"mocked\":true}"}}`)); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
	}
	<-done

	// truncated 是非导出字段,只能经写线行为观察:长度必须按新 body 重算。
	var buf bytes.Buffer
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	if err := flow.WriteResponse(&buf, f, req); err != nil {
		t.Fatalf("WriteResponse 失败: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "Content-Length: 15") {
		t.Errorf("换过 body 后应按新内容算长度:\n%s", got)
	}
}
