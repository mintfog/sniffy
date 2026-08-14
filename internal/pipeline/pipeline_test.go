// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// ---- 测试替身 ----

// stubHook 提供 Hook 的四个基础方法,供各具体钩子类型嵌入。拆成多个类型而非一个大替身:
// Register 按接口断言分类,单一类型满足全部接口就无法验证「只有某类钩子被调用」。
type stubHook struct {
	name     string
	priority int
	disabled bool
	matchFn  func(url string) bool
}

func (h stubHook) Name() string  { return h.name }
func (h stubHook) Priority() int { return h.priority }
func (h stubHook) Enabled() bool { return !h.disabled }
func (h stubHook) Match(url string) bool {
	if h.matchFn == nil {
		return true
	}
	return h.matchFn(url)
}

type reqHook struct {
	stubHook
	fn func(*flow.Flow) flow.Decision
}

func (h *reqHook) OnRequest(_ context.Context, f *flow.Flow) flow.Decision {
	if h.fn == nil {
		return flow.ContinueDecision()
	}
	return h.fn(f)
}

type respHook struct {
	stubHook
	fn func(*flow.Flow) flow.Decision
}

func (h *respHook) OnResponse(_ context.Context, f *flow.Flow) flow.Decision {
	if h.fn == nil {
		return flow.ContinueDecision()
	}
	return h.fn(f)
}

type wsHook struct {
	stubHook
	fn func(*flow.WSMessage) flow.Decision
}

func (h *wsHook) OnWebSocketMessage(_ context.Context, m *flow.WSMessage) flow.Decision {
	if h.fn == nil {
		return flow.ContinueDecision()
	}
	return h.fn(m)
}

type streamHook struct {
	stubHook
	fn func(*flow.StreamMessage) flow.Decision
}

func (h *streamHook) OnStreamMessage(_ context.Context, m *flow.StreamMessage) flow.Decision {
	if h.fn == nil {
		return flow.ContinueDecision()
	}
	return h.fn(m)
}

type connHook struct {
	stubHook
}

func (h *connHook) OnConnect(_ context.Context, _, _ string) {}
func (h *connHook) OnDisconnect(_ context.Context, _ string) {}

// recorder 按调用顺序记录钩子名。并发用例也会用到,故加锁。
type recorder struct {
	mu    sync.Mutex
	names []string
}

func (r *recorder) add(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
}

func (r *recorder) order() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.names, ",")
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.names)
}

// rec* 构造「记录调用并返回固定处置」的钩子,是绝大多数用例需要的形状。
func recReq(r *recorder, name string, priority int, d flow.Decision) *reqHook {
	return &reqHook{
		stubHook: stubHook{name: name, priority: priority},
		fn:       func(*flow.Flow) flow.Decision { r.add(name); return d },
	}
}

func recResp(r *recorder, name string, priority int, d flow.Decision) *respHook {
	return &respHook{
		stubHook: stubHook{name: name, priority: priority},
		fn:       func(*flow.Flow) flow.Decision { r.add(name); return d },
	}
}

func recWS(r *recorder, name string, priority int, d flow.Decision) *wsHook {
	return &wsHook{
		stubHook: stubHook{name: name, priority: priority},
		fn:       func(*flow.WSMessage) flow.Decision { r.add(name); return d },
	}
}

func recStream(r *recorder, name string, priority int, d flow.Decision) *streamHook {
	return &streamHook{
		stubHook: stubHook{name: name, priority: priority},
		fn:       func(*flow.StreamMessage) flow.Decision { r.add(name); return d },
	}
}

// logEntry 保留原始 msg 与 args,断言只看 args 里有无插件名,不与格式化写法耦合。
type logEntry struct {
	msg  string
	args []any
}

type capturingLogger struct {
	mu     sync.Mutex
	errors []logEntry
}

func (l *capturingLogger) Debug(string, ...any) {}

func (l *capturingLogger) Error(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, logEntry{msg: msg, args: args})
}

// mentions 判断是否记录过提及 name 的错误日志。
func (l *capturingLogger) mentions(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.errors {
		for _, a := range e.args {
			if s, ok := a.(string); ok && s == name {
				return true
			}
		}
	}
	return false
}

// ---- 通用辅助 ----

func newReqFlow() *flow.Flow {
	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{Method: "GET", URL: "https://x.com/", Host: "x.com", Path: "/", Header: map[string][]string{}}
	return f
}

func newRespFlow() *flow.Flow {
	f := newReqFlow()
	f.Response = &flow.Response{Status: 200, Header: map[string][]string{}, Body: []byte("ok")}
	return f
}

// runAsync 在独立 goroutine 中求值处置:断点会同步挂起调用方,须由用例另行放行。
func runAsync(fn func() flow.Decision) <-chan flow.Decision {
	ch := make(chan flow.Decision, 1)
	go func() { ch <- fn() }()
	return ch
}

func waitDecision(t *testing.T, ch <-chan flow.Decision) flow.Decision {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(3 * time.Second):
		t.Fatal("等待处置超时:断点未被放行")
		return flow.Decision{}
	}
}

// waitPaused 轮询等待 id 进入暂停列表。只能在用例主 goroutine 中调用(内部会 Fatal)。
func waitPaused(t *testing.T, bm *BreakpointManager, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range bm.List() {
			if f.ID == id {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("flow %s 未在超时前进入断点暂停", id)
}

// ---- 注册与排序 ----

// 核心钩子与插件钩子在 snapshot 时按优先级统一排序,而非「核心永远在前」。
func TestSnapshotMergesCoreAndPluginByPriority(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	p.RegisterCore(recReq(rec, "core-late", 10, flow.ContinueDecision()))
	p.Register(recReq(rec, "plugin-early", 1, flow.ContinueDecision()))
	p.RegisterCore(recReq(rec, "core-early", 0, flow.ContinueDecision()))
	p.Register(recReq(rec, "plugin-late", 5, flow.ContinueDecision()))

	p.OnRequest(context.Background(), newReqFlow())

	const want = "core-early,plugin-early,plugin-late,core-late"
	if got := rec.order(); got != want {
		t.Errorf("执行顺序 = %q, want %q", got, want)
	}
}

// 同优先级必须保持注册顺序(SliceStable),否则插件行为会随注册次序抖动。
func TestSamePriorityKeepsRegistrationOrder(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	for _, name := range []string{"a", "b", "c", "d"} {
		p.Register(recReq(rec, name, 7, flow.ContinueDecision()))
	}

	p.OnRequest(context.Background(), newReqFlow())

	if got, want := rec.order(), "a,b,c,d"; got != want {
		t.Errorf("同优先级执行顺序 = %q, want %q", got, want)
	}
}

// Clear() 模拟插件热重载:插件钩子全清,核心钩子在所有类型上都必须存活。
func TestClearRemovesPluginHooksKeepsCore(t *testing.T) {
	p := New(nil, nil)
	core, plugin := &recorder{}, &recorder{}

	p.RegisterCore(recReq(core, "core-req", 0, flow.ContinueDecision()))
	p.RegisterCore(recResp(core, "core-resp", 0, flow.ContinueDecision()))
	p.RegisterCore(recWS(core, "core-ws", 0, flow.ContinueDecision()))
	p.RegisterCore(recStream(core, "core-stream", 0, flow.ContinueDecision()))

	p.Register(recReq(plugin, "plugin-req", 1, flow.ContinueDecision()))
	p.Register(recResp(plugin, "plugin-resp", 1, flow.ContinueDecision()))
	p.Register(recWS(plugin, "plugin-ws", 1, flow.ContinueDecision()))
	p.Register(recStream(plugin, "plugin-stream", 1, flow.ContinueDecision()))

	fire := func() {
		ctx := context.Background()
		p.OnRequest(ctx, newReqFlow())
		p.OnResponse(ctx, newRespFlow())
		p.OnWebSocketMessage(ctx, &flow.WSMessage{URL: "https://x.com/"})
		p.OnStreamMessage(ctx, &flow.StreamMessage{URL: "https://x.com/"})
	}

	fire()
	if core.count() != 4 || plugin.count() != 4 {
		t.Fatalf("热重载前 core=%d plugin=%d, want 4/4", core.count(), plugin.count())
	}

	p.Clear()
	fire()

	if core.count() != 8 {
		t.Errorf("核心钩子应在 Clear() 后存活: core=%d, want 8", core.count())
	}
	if plugin.count() != 4 {
		t.Errorf("插件钩子应被 Clear() 清除: plugin=%d, want 4", plugin.count())
	}
}

// ConnHook 只被 Register 收集,管道里没有派发点,故只钉住注册与 Clear() 的簿记。
func TestConnHookRegisteredButNeverDispatched(t *testing.T) {
	p := New(nil, nil)
	p.Register(&connHook{stubHook: stubHook{name: "conn", priority: 0}})

	if n := len(p.connHooks); n != 1 {
		t.Fatalf("connHooks 长度 = %d, want 1", n)
	}

	p.Clear()
	if n := len(p.connHooks); n != 0 {
		t.Errorf("Clear() 后 connHooks 长度 = %d, want 0", n)
	}
}

// snapshot 返回的是副本,迭代期间的并发注册/热重载不应触发竞态或改变处置。
func TestSnapshotIsolatedFromConcurrentRegister(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	p.RegisterCore(recReq(rec, "core", 2, flow.ContinueDecision()))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// 只增删插件钩子:核心钩子不受 Clear() 影响,循环注册会让快照无限膨胀。
			p.Register(recReq(rec, "churn", i%4, flow.ContinueDecision()))
			p.Clear()
		}
	}()
	defer func() {
		close(stop)
		wg.Wait()
	}()

	for i := 0; i < 200; i++ {
		if d := p.OnRequest(context.Background(), newReqFlow()); d.Kind != flow.Continue {
			t.Fatalf("并发注册期间处置 = %v, want continue", d.Kind)
		}
	}
}

// ---- 短路与合并 ----

func TestOnRequestAbortShortCircuits(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	p.Register(recReq(rec, "first", 0, flow.AbortDecision(403, "blocked")))
	p.Register(recReq(rec, "second", 1, flow.ContinueDecision()))

	d := p.OnRequest(context.Background(), newReqFlow())

	if d.Kind != flow.Abort || d.StatusOnAbort != 403 || d.Reason != "blocked" {
		t.Errorf("处置 = %+v, want abort/403/blocked", d)
	}
	if got := rec.order(); got != "first" {
		t.Errorf("Abort 后应短路: 调用序列 = %q, want %q", got, "first")
	}
}

func TestOnRequestMockShortCircuits(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	p.Register(recReq(rec, "first", 0, flow.MockDecision("mocked")))
	p.Register(recReq(rec, "second", 1, flow.ContinueDecision()))

	d := p.OnRequest(context.Background(), newReqFlow())

	if d.Kind != flow.Mock || d.Reason != "mocked" {
		t.Errorf("处置 = %+v, want mock/mocked", d)
	}
	if got := rec.order(); got != "first" {
		t.Errorf("Mock 后应短路: 调用序列 = %q, want %q", got, "first")
	}
}

func TestOnResponseAbortShortCircuits(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	p.Register(recResp(rec, "first", 0, flow.AbortDecision(0, "kill")))
	p.Register(recResp(rec, "second", 1, flow.ContinueDecision()))

	d := p.OnResponse(context.Background(), newRespFlow())

	if d.Kind != flow.Abort {
		t.Errorf("处置 = %v, want abort", d.Kind)
	}
	if got := rec.order(); got != "first" {
		t.Errorf("Abort 后应短路: 调用序列 = %q, want %q", got, "first")
	}
}

// 响应阶段没有 Mock 语义:Mock 不短路,后续钩子照常执行,但合并结果仍保留 Mock 优先级。
func TestOnResponseMockDoesNotShortCircuit(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	p.Register(recResp(rec, "first", 0, flow.MockDecision("m")))
	p.Register(recResp(rec, "second", 1, flow.ContinueDecision()))

	d := p.OnResponse(context.Background(), newRespFlow())

	if got := rec.order(); got != "first,second" {
		t.Errorf("响应阶段 Mock 不应短路: 调用序列 = %q, want %q", got, "first,second")
	}
	if d.Kind != flow.Mock {
		t.Errorf("合并处置 = %v, want mock(优先级高于 continue)", d.Kind)
	}
}

// 只验证 Continue 不会盖掉更高优先级;Breakpoint 的挂起路径另有用例。
func TestMergeKeepsHighestPriorityAcrossHooks(t *testing.T) {
	cases := []struct {
		name  string
		first flow.Decision
		want  flow.DecisionKind
	}{
		{"continue 保持 continue", flow.ContinueDecision(), flow.Continue},
		{"mock 不被后续 continue 覆盖", flow.MockDecision("m"), flow.Mock},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := New(nil, nil)
			rec := &recorder{}
			p.Register(recResp(rec, "a", 0, c.first))
			p.Register(recResp(rec, "b", 1, flow.ContinueDecision()))

			if d := p.OnResponse(context.Background(), newRespFlow()); d.Kind != c.want {
				t.Errorf("处置 = %v, want %v", d.Kind, c.want)
			}
		})
	}
}

// ---- 门控:Enabled / Match ----

func TestDisabledAndUnmatchedHooksAreSkipped(t *testing.T) {
	never := func(string) bool { return false }

	t.Run("请求", func(t *testing.T) {
		p := New(nil, nil)
		rec := &recorder{}
		off := recReq(rec, "off", 0, flow.AbortDecision(500, "x"))
		off.disabled = true
		unmatched := recReq(rec, "unmatched", 1, flow.AbortDecision(500, "x"))
		unmatched.matchFn = never
		p.Register(off)
		p.Register(unmatched)
		p.Register(recReq(rec, "on", 2, flow.ContinueDecision()))

		if d := p.OnRequest(context.Background(), newReqFlow()); d.Kind != flow.Continue {
			t.Errorf("处置 = %v, want continue(被跳过的钩子不应生效)", d.Kind)
		}
		if got := rec.order(); got != "on" {
			t.Errorf("调用序列 = %q, want %q", got, "on")
		}
	})

	t.Run("响应", func(t *testing.T) {
		p := New(nil, nil)
		rec := &recorder{}
		off := recResp(rec, "off", 0, flow.AbortDecision(500, "x"))
		off.disabled = true
		unmatched := recResp(rec, "unmatched", 1, flow.AbortDecision(500, "x"))
		unmatched.matchFn = never
		p.Register(off)
		p.Register(unmatched)
		p.Register(recResp(rec, "on", 2, flow.ContinueDecision()))

		if d := p.OnResponse(context.Background(), newRespFlow()); d.Kind != flow.Continue {
			t.Errorf("处置 = %v, want continue", d.Kind)
		}
		if got := rec.order(); got != "on" {
			t.Errorf("调用序列 = %q, want %q", got, "on")
		}
	})

	t.Run("websocket", func(t *testing.T) {
		p := New(nil, nil)
		rec := &recorder{}
		off := recWS(rec, "off", 0, flow.AbortDecision(0, "x"))
		off.disabled = true
		unmatched := recWS(rec, "unmatched", 1, flow.AbortDecision(0, "x"))
		unmatched.matchFn = never
		p.Register(off)
		p.Register(unmatched)
		p.Register(recWS(rec, "on", 2, flow.ContinueDecision()))

		if d := p.OnWebSocketMessage(context.Background(), &flow.WSMessage{URL: "https://x.com/"}); d.Kind != flow.Continue {
			t.Errorf("处置 = %v, want continue", d.Kind)
		}
		if got := rec.order(); got != "on" {
			t.Errorf("调用序列 = %q, want %q", got, "on")
		}
	})

	t.Run("流", func(t *testing.T) {
		p := New(nil, nil)
		rec := &recorder{}
		off := recStream(rec, "off", 0, flow.AbortDecision(0, "x"))
		off.disabled = true
		unmatched := recStream(rec, "unmatched", 1, flow.AbortDecision(0, "x"))
		unmatched.matchFn = never
		p.Register(off)
		p.Register(unmatched)
		p.Register(recStream(rec, "on", 2, flow.ContinueDecision()))

		if d := p.OnStreamMessage(context.Background(), &flow.StreamMessage{URL: "https://x.com/"}); d.Kind != flow.Continue {
			t.Errorf("处置 = %v, want continue", d.Kind)
		}
		if got := rec.order(); got != "on" {
			t.Errorf("调用序列 = %q, want %q", got, "on")
		}
	})
}

// Match 拿到的 URL 取自 f.Request.URL;WS/流则取消息自身的 URL。
func TestMatchReceivesFlowURL(t *testing.T) {
	p := New(nil, nil)
	var seen string
	h := &reqHook{stubHook: stubHook{name: "spy", matchFn: func(url string) bool { seen = url; return true }}}
	p.Register(h)

	p.OnRequest(context.Background(), newReqFlow())

	if seen != "https://x.com/" {
		t.Errorf("Match 收到的 URL = %q, want %q", seen, "https://x.com/")
	}
}

// Flow.Request 为 nil(连接建立即出错等场景)时按空 URL 门控,不能 panic。
func TestNilRequestPassesEmptyURL(t *testing.T) {
	p := New(nil, nil)
	// 初值取哨兵而非空串:否则钩子根本没被调用时 seen 仍是 ""，断言会恒真。
	seenReq, seenResp := "未调用", "未调用"
	p.Register(&reqHook{stubHook: stubHook{name: "req", matchFn: func(u string) bool { seenReq = u; return true }}})
	p.Register(&respHook{stubHook: stubHook{name: "resp", matchFn: func(u string) bool { seenResp = u; return true }}})

	f := flow.New(flow.ProtoHTTP) // 无 Request
	if d := p.OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Errorf("请求处置 = %v, want continue", d.Kind)
	}
	if d := p.OnResponse(context.Background(), f); d.Kind != flow.Continue {
		t.Errorf("响应处置 = %v, want continue", d.Kind)
	}
	if seenReq != "" || seenResp != "" {
		t.Errorf("Request 为 nil 时应传空 URL: req=%q resp=%q", seenReq, seenResp)
	}
}

// ---- panic 失败开放 ----

// 插件 panic 必须被捕获、记录,并按 Continue 放行,且不影响后续钩子执行。
func TestHookPanicFailsOpen(t *testing.T) {
	t.Run("请求", func(t *testing.T) {
		lg := &capturingLogger{}
		p := New(nil, lg)
		rec := &recorder{}
		p.Register(&reqHook{
			stubHook: stubHook{name: "boom", priority: 0},
			fn:       func(*flow.Flow) flow.Decision { panic("请求插件炸了") },
		})
		p.Register(recReq(rec, "after", 1, flow.ContinueDecision()))

		if d := p.OnRequest(context.Background(), newReqFlow()); d.Kind != flow.Continue {
			t.Errorf("处置 = %v, want continue(失败开放)", d.Kind)
		}
		if got := rec.order(); got != "after" {
			t.Errorf("panic 不应中断后续钩子: 调用序列 = %q", got)
		}
		if !lg.mentions("boom") {
			t.Error("panic 应被记录到错误日志")
		}
	})

	t.Run("响应", func(t *testing.T) {
		lg := &capturingLogger{}
		p := New(nil, lg)
		rec := &recorder{}
		p.Register(&respHook{
			stubHook: stubHook{name: "boom", priority: 0},
			fn:       func(*flow.Flow) flow.Decision { panic("响应插件炸了") },
		})
		p.Register(recResp(rec, "after", 1, flow.ContinueDecision()))

		if d := p.OnResponse(context.Background(), newRespFlow()); d.Kind != flow.Continue {
			t.Errorf("处置 = %v, want continue", d.Kind)
		}
		if got := rec.order(); got != "after" {
			t.Errorf("panic 不应中断后续钩子: 调用序列 = %q", got)
		}
		if !lg.mentions("boom") {
			t.Error("panic 应被记录到错误日志")
		}
	})

	t.Run("websocket", func(t *testing.T) {
		lg := &capturingLogger{}
		p := New(nil, lg)
		rec := &recorder{}
		p.Register(&wsHook{
			stubHook: stubHook{name: "boom", priority: 0},
			fn:       func(*flow.WSMessage) flow.Decision { panic("ws 插件炸了") },
		})
		p.Register(recWS(rec, "after", 1, flow.ContinueDecision()))

		if d := p.OnWebSocketMessage(context.Background(), &flow.WSMessage{URL: "u"}); d.Kind != flow.Continue {
			t.Errorf("处置 = %v, want continue", d.Kind)
		}
		if got := rec.order(); got != "after" {
			t.Errorf("panic 不应中断后续钩子: 调用序列 = %q", got)
		}
		if !lg.mentions("boom") {
			t.Error("panic 应被记录到错误日志")
		}
	})

	t.Run("流", func(t *testing.T) {
		lg := &capturingLogger{}
		p := New(nil, lg)
		rec := &recorder{}
		p.Register(&streamHook{
			stubHook: stubHook{name: "boom", priority: 0},
			fn:       func(*flow.StreamMessage) flow.Decision { panic("流插件炸了") },
		})
		p.Register(recStream(rec, "after", 1, flow.ContinueDecision()))

		if d := p.OnStreamMessage(context.Background(), &flow.StreamMessage{URL: "u"}); d.Kind != flow.Continue {
			t.Errorf("处置 = %v, want continue", d.Kind)
		}
		if got := rec.order(); got != "after" {
			t.Errorf("panic 不应中断后续钩子: 调用序列 = %q", got)
		}
		if !lg.mentions("boom") {
			t.Error("panic 应被记录到错误日志")
		}
	})
}

// panic 已经把处置降级为 Continue 后,后续钩子的更高优先级处置仍应生效。
func TestPanicDoesNotSuppressLaterDecision(t *testing.T) {
	p := New(nil, &capturingLogger{})
	p.Register(&reqHook{
		stubHook: stubHook{name: "boom", priority: 0},
		fn:       func(*flow.Flow) flow.Decision { panic("x") },
	})
	p.Register(recReq(&recorder{}, "abort", 1, flow.AbortDecision(451, "later")))

	d := p.OnRequest(context.Background(), newReqFlow())
	if d.Kind != flow.Abort || d.StatusOnAbort != 451 {
		t.Errorf("处置 = %+v, want abort/451", d)
	}
}

// logger 为 nil 时退化为 nopLogger,panic 路径不能因日志再次 panic。
func TestNilLoggerToleratesPanic(t *testing.T) {
	p := New(nil, nil)
	p.Register(&reqHook{
		stubHook: stubHook{name: "boom"},
		fn:       func(*flow.Flow) flow.Decision { panic("x") },
	})

	if d := p.OnRequest(context.Background(), newReqFlow()); d.Kind != flow.Continue {
		t.Errorf("处置 = %v, want continue", d.Kind)
	}
}

// ---- WebSocket / 流消息 ----

// WS 钩子就地改写 m.Data,改动对后续钩子与调用方都可见。
func TestOnWebSocketMessageMutatesDataInPlace(t *testing.T) {
	p := New(nil, nil)
	p.Register(&wsHook{
		stubHook: stubHook{name: "upper", priority: 0},
		fn: func(m *flow.WSMessage) flow.Decision {
			m.Data = []byte(strings.ToUpper(string(m.Data)))
			return flow.ContinueDecision()
		},
	})
	var seenBySecond string
	p.Register(&wsHook{
		stubHook: stubHook{name: "suffix", priority: 1},
		fn: func(m *flow.WSMessage) flow.Decision {
			seenBySecond = string(m.Data)
			m.Data = append(m.Data, '!')
			return flow.ContinueDecision()
		},
	})

	m := &flow.WSMessage{URL: "wss://x.com/", Type: flow.WSText, Data: []byte("hi")}
	p.OnWebSocketMessage(context.Background(), m)

	if seenBySecond != "HI" {
		t.Errorf("第二个钩子看到的 Data = %q, want %q", seenBySecond, "HI")
	}
	if got := string(m.Data); got != "HI!" {
		t.Errorf("最终 Data = %q, want %q", got, "HI!")
	}
}

func TestOnWebSocketMessageAbortShortCircuits(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	p.Register(recWS(rec, "first", 0, flow.AbortDecision(0, "drop")))
	p.Register(recWS(rec, "second", 1, flow.ContinueDecision()))

	d := p.OnWebSocketMessage(context.Background(), &flow.WSMessage{URL: "wss://x.com/"})

	if d.Kind != flow.Abort || d.Reason != "drop" {
		t.Errorf("处置 = %+v, want abort/drop", d)
	}
	if got := rec.order(); got != "first" {
		t.Errorf("Abort 后应短路: 调用序列 = %q", got)
	}
}

func TestOnStreamMessageMutatesDataInPlace(t *testing.T) {
	p := New(nil, nil)
	p.Register(&streamHook{
		stubHook: stubHook{name: "rewrite", priority: 0},
		fn: func(m *flow.StreamMessage) flow.Decision {
			m.Data = []byte("replaced")
			return flow.ContinueDecision()
		},
	})

	m := &flow.StreamMessage{URL: "https://x.com/sse", Kind: flow.StreamSSE, Data: []byte("data: 1")}
	if d := p.OnStreamMessage(context.Background(), m); d.Kind != flow.Continue {
		t.Errorf("处置 = %v, want continue", d.Kind)
	}
	if got := string(m.Data); got != "replaced" {
		t.Errorf("Data = %q, want %q", got, "replaced")
	}
}

// 流钩子返回 Abort 表示提前终止该流,后续钩子不再执行。
func TestOnStreamMessageAbortShortCircuits(t *testing.T) {
	p := New(nil, nil)
	rec := &recorder{}
	p.Register(recStream(rec, "first", 0, flow.AbortDecision(0, "stop")))
	p.Register(recStream(rec, "second", 1, flow.ContinueDecision()))

	d := p.OnStreamMessage(context.Background(), &flow.StreamMessage{URL: "https://x.com/sse"})

	if d.Kind != flow.Abort || d.Reason != "stop" {
		t.Errorf("处置 = %+v, want abort/stop", d)
	}
	if got := rec.order(); got != "first" {
		t.Errorf("Abort 后应短路: 调用序列 = %q", got)
	}
}

// 无任何钩子时,WS/流消息原样放行。
func TestNoHooksPassesThrough(t *testing.T) {
	p := New(nil, nil)
	ctx := context.Background()

	if d := p.OnWebSocketMessage(ctx, &flow.WSMessage{URL: "u", Data: []byte("x")}); d.Kind != flow.Continue {
		t.Errorf("ws 处置 = %v, want continue", d.Kind)
	}
	if d := p.OnStreamMessage(ctx, &flow.StreamMessage{URL: "u", Data: []byte("x")}); d.Kind != flow.Continue {
		t.Errorf("流处置 = %v, want continue", d.Kind)
	}
	if d := p.OnRequest(ctx, newReqFlow()); d.Kind != flow.Continue {
		t.Errorf("请求处置 = %v, want continue", d.Kind)
	}
	if d := p.OnResponse(ctx, newRespFlow()); d.Kind != flow.Continue {
		t.Errorf("响应处置 = %v, want continue", d.Kind)
	}
}

// ---- 断点与管道的衔接 ----

// 钩子返回 Breakpoint → 同步挂起 → UI 放行(带编辑)→ 管道返回 Continue。
func TestOnRequestBreakpointResumeContinue(t *testing.T) {
	p := New(nil, nil)
	p.Register(recReq(&recorder{}, "bp", 0, flow.BreakpointDecision(flow.PhaseRequest, "手动")))

	f := newReqFlow()
	ch := runAsync(func() flow.Decision { return p.OnRequest(context.Background(), f) })
	waitPaused(t, p.Breakpoints(), f.ID)

	edited := f.Clone()
	edited.Request.URL = "https://edited.example/"
	if !p.Breakpoints().Resume(f.ID, edited) {
		t.Fatal("Resume 应成功")
	}

	if d := waitDecision(t, ch); d.Kind != flow.Continue {
		t.Errorf("放行后处置 = %v, want continue", d.Kind)
	}
	if f.Request.URL != "https://edited.example/" {
		t.Errorf("编辑未合并回原 flow: URL = %q", f.Request.URL)
	}
	if !f.Modified {
		t.Error("带编辑放行应把 flow 标记为 Modified")
	}
	if f.State != flow.StatePending {
		t.Errorf("放行后状态 = %q, want %q(恢复暂停前的状态)", f.State, flow.StatePending)
	}
}

// UI 选择阻断 → 管道把它翻译成 Abort 处置。
func TestOnRequestBreakpointResumeAbort(t *testing.T) {
	p := New(nil, nil)
	p.Register(recReq(&recorder{}, "bp", 0, flow.BreakpointDecision(flow.PhaseRequest, "手动")))

	f := newReqFlow()
	ch := runAsync(func() flow.Decision { return p.OnRequest(context.Background(), f) })
	waitPaused(t, p.Breakpoints(), f.ID)

	if !p.Breakpoints().Abort(f.ID) {
		t.Fatal("Abort 应成功")
	}

	d := waitDecision(t, ch)
	if d.Kind != flow.Abort || d.Reason != "aborted at breakpoint" {
		t.Errorf("处置 = %+v, want abort/aborted at breakpoint", d)
	}
}

// 没有任何钩子时,全局「断在请求」开关本身就应触发挂起。
func TestOnRequestGlobalBreakPauses(t *testing.T) {
	p := New(nil, nil)
	p.Breakpoints().SetGlobalBreak(true, false)

	f := newReqFlow()
	ch := runAsync(func() flow.Decision { return p.OnRequest(context.Background(), f) })
	waitPaused(t, p.Breakpoints(), f.ID)

	if !p.Breakpoints().Resume(f.ID, nil) {
		t.Fatal("Resume 应成功")
	}
	if d := waitDecision(t, ch); d.Kind != flow.Continue {
		t.Errorf("处置 = %v, want continue", d.Kind)
	}
}

func TestOnResponseGlobalBreakPauses(t *testing.T) {
	p := New(nil, nil)
	p.Breakpoints().SetGlobalBreak(false, true)

	f := newRespFlow()
	ch := runAsync(func() flow.Decision { return p.OnResponse(context.Background(), f) })
	waitPaused(t, p.Breakpoints(), f.ID)

	edited := f.Clone()
	edited.Response.Status = 503
	if !p.Breakpoints().Resume(f.ID, edited) {
		t.Fatal("Resume 应成功")
	}

	if d := waitDecision(t, ch); d.Kind != flow.Continue {
		t.Errorf("处置 = %v, want continue", d.Kind)
	}
	if f.Response.Status != 503 {
		t.Errorf("响应编辑未合并: Status = %d, want 503", f.Response.Status)
	}
}

// 响应阶段的断点同样支持阻断。
func TestOnResponseBreakpointResumeAbort(t *testing.T) {
	p := New(nil, nil)
	p.Breakpoints().SetGlobalBreak(false, true)

	f := newRespFlow()
	ch := runAsync(func() flow.Decision { return p.OnResponse(context.Background(), f) })
	waitPaused(t, p.Breakpoints(), f.ID)

	if !p.Breakpoints().Abort(f.ID) {
		t.Fatal("Abort 应成功")
	}

	d := waitDecision(t, ch)
	if d.Kind != flow.Abort || d.Reason != "aborted at breakpoint" {
		t.Errorf("处置 = %+v, want abort/aborted at breakpoint", d)
	}
}

// 短路处置(Abort/Mock)发生在断点检查之前:即使全局开关打开也不应挂起。
func TestShortCircuitSkipsBreakpointCheck(t *testing.T) {
	cases := []struct {
		name string
		d    flow.Decision
		want flow.DecisionKind
	}{
		{"abort", flow.AbortDecision(403, "x"), flow.Abort},
		{"mock", flow.MockDecision("x"), flow.Mock},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := New(nil, nil)
			p.Breakpoints().SetGlobalBreak(true, true)
			p.Register(recReq(&recorder{}, "short", 0, c.d))

			f := newReqFlow()
			ch := runAsync(func() flow.Decision { return p.OnRequest(context.Background(), f) })

			if d := waitDecision(t, ch); d.Kind != c.want {
				t.Errorf("处置 = %v, want %v", d.Kind, c.want)
			}
			if n := len(p.Breakpoints().List()); n != 0 {
				t.Errorf("不应有暂停中的 flow: got %d", n)
			}
			if f.State == flow.StatePausedAtBreakpoint {
				t.Error("短路的 flow 不应停留在断点状态")
			}
		})
	}
}

// URL 断点规则命中时也应挂起,且只作用于规则覆盖的阶段。
func TestOnRequestURLRulePauses(t *testing.T) {
	p := New(nil, nil)
	p.Breakpoints().AddRule("https://x.com/*", true, false)

	f := newReqFlow()
	ch := runAsync(func() flow.Decision { return p.OnRequest(context.Background(), f) })
	waitPaused(t, p.Breakpoints(), f.ID)
	if !p.Breakpoints().Resume(f.ID, nil) {
		t.Fatal("Resume 应成功")
	}
	if d := waitDecision(t, ch); d.Kind != flow.Continue {
		t.Errorf("处置 = %v, want continue", d.Kind)
	}

	// 规则未覆盖响应阶段:OnResponse 必须直接返回,不挂起。
	done := runAsync(func() flow.Decision { return p.OnResponse(context.Background(), newRespFlow()) })
	if d := waitDecision(t, done); d.Kind != flow.Continue {
		t.Errorf("响应处置 = %v, want continue(规则不覆盖响应阶段)", d.Kind)
	}
}
