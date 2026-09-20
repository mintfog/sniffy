// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package js

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// rtBusyLoop 生成按墙钟时间运行的顶层或钩子代码。
func rtBusyLoop(ms int) string {
	return fmt.Sprintf("var __t0=Date.now(); while(Date.now()-__t0 < %d){}", ms)
}

// rtReadJSON 读取并解析 JSON 对象状态文件。
func rtReadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s 内容不是合法 JSON 对象: %v (原始内容 %q)", path, err, string(data))
	}
	return m
}

// rtHasErrLog 判断插件日志里是否有一条 error 级记录同时含全部关键词。
func rtHasErrLog(p *Plugin, parts ...string) bool {
	for _, e := range p.Logs() {
		if e.Level != "error" {
			continue
		}
		hit := true
		for _, want := range parts {
			if !strings.Contains(e.Msg, want) {
				hit = false
			}
		}
		if hit {
			return true
		}
	}
	return false
}

// 验证单次钩子超时返回 Continue 并保留原始 flow。
func TestRuntimeHookTimeoutFailsOpen(t *testing.T) {
	p := mustPlugin(t, Config{
		ID:      "rt-timeout",
		Source:  "function onRequest(f){ header.set(f.headers,'X-Touched','1'); " + rtBusyLoop(60_000) + " }",
		Timeout: 20 * time.Millisecond,
	})
	f := newReqFlow()
	start := time.Now()
	d := p.OnRequest(context.Background(), f)
	elapsed := time.Since(start)

	if d.Kind != flow.Continue {
		t.Fatalf("超时后处置 = %v,应为 Continue", d.Kind)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("死循环钩子耗时 %v,未在 20ms 量级被打断", elapsed)
	}
	if f.Modified {
		t.Fatal("超时的钩子不得把 flow 标记为 Modified")
	}
	if got, ok := f.Request.Header["X-Touched"]; ok {
		t.Fatalf("超时钩子的中途改动泄漏进 flow: X-Touched=%v", got)
	}
	var errLogged bool
	for _, e := range p.Logs() {
		if e.Level == "error" && strings.Contains(e.Msg, "runtime error") {
			errLogged = true
		}
	}
	if !errLogged {
		t.Fatalf("超时未产生 error 级日志,作者无从得知插件被打断: %+v", p.Logs())
	}
}

// 验证 VM 中断后同一插件实例仍可处理后续请求。
func TestRuntimeVMUsableAfterInterrupt(t *testing.T) {
	src := "function onRequest(f){ if (header.has(f.headers,'X-Spin')) { " + rtBusyLoop(60_000) + " } header.set(f.headers,'X-Ok','1'); }"
	p := mustPlugin(t, Config{ID: "rt-reuse", Source: src, Timeout: 20 * time.Millisecond})

	spun := newReqFlow()
	spun.Request.Header["X-Spin"] = []string{"1"}
	if d := p.OnRequest(context.Background(), spun); d.Kind != flow.Continue {
		t.Fatalf("超时请求处置 = %v,应为 Continue", d.Kind)
	}

	for i := 0; i < 3; i++ {
		f := newReqFlow()
		if d := p.OnRequest(context.Background(), f); d.Kind != flow.Continue {
			t.Fatalf("第 %d 次恢复请求处置 = %v", i, d.Kind)
		}
		if got := f.Request.Header["X-Ok"]; len(got) != 1 || got[0] != "1" {
			t.Fatalf("中断后第 %d 次调用未生效,X-Ok=%v(VM 已变砖)", i, got)
		}
	}
}

// 验证一次运行的超时中断仅作用于对应代次，后续调用按自身预算运行。
func TestRuntimeInterruptDoesNotLeakToNextCall(t *testing.T) {
	const timeout = 10 * time.Millisecond
	src := "function onRequest(f){ if (header.has(f.headers,'X-Spin')) { " + rtBusyLoop(10) +
		" } header.set(f.headers,'X-Ok','1'); }"
	p := mustPlugin(t, Config{ID: "rt-gen", Source: src, Timeout: timeout})

	for i := 0; i < 200; i++ {
		spin := newReqFlow()
		spin.Request.Header["X-Spin"] = []string{"1"}
		// 该调用用于触发超时回调与 ClearInterrupt 的并发窗口。
		p.OnRequest(context.Background(), spin)

		f := newReqFlow()
		start := time.Now()
		p.OnRequest(context.Background(), f)
		elapsed := time.Since(start)
		if len(f.Request.Header["X-Ok"]) == 0 && elapsed < timeout {
			t.Fatalf("第 %d 轮:紧跟跑满预算那次的调用在 %v 内就被打断(自身预算 %v 远未用完),"+
				"上一次的中断标志泄漏了", i, elapsed, timeout)
		}
	}
}

// 验证顶层求值的中断状态不会泄漏到实例的第一次钩子调用。
func TestRuntimeInitInterruptDoesNotLeakToFirstCall(t *testing.T) {
	if testing.Short() {
		t.Skip("需真实等待顶层求值的中断上限")
	}
	t.Parallel()
	const timeout = 20 * time.Millisecond // initTimeout 取下限 1s

	tested := 0
	// 700ms 档用于确保至少完成一次钩子调用，其余档位覆盖顶层求值上限附近的时序。
	for _, ms := range []int{700, 985, 1000, 1010} {
		src := rtBusyLoop(ms) + "\nfunction onRequest(f){ header.set(f.headers,'X-Ok','1'); }"
		p, err := NewPlugin(Config{ID: "rt-init-gen", Enabled: true, Source: src, Timeout: timeout}, nil)
		if err != nil {
			continue
		}
		f := newReqFlow()
		start := time.Now()
		p.OnRequest(context.Background(), f)
		elapsed := time.Since(start)
		p.Close()
		if len(f.Request.Header["X-Ok"]) == 0 && elapsed < timeout {
			t.Fatalf("顶层耗时 %dms 的实例:第一次钩子调用在 %v 内就被打断(自身预算 %v 远未用完),"+
				"初始化超时的中断标志泄漏了", ms, elapsed, timeout)
		}
		tested++
	}
	if tested == 0 {
		// 顶层求值均达到中断上限时记录环境限制并结束该档位。
		t.Skip("本环境下四档顶层求值均未通过中断上限,无实例可测")
	}
}

// 验证脚本异常的处置、已完成改动及 error 日志记录。
func TestRuntimeUncaughtExceptionFailsOpen(t *testing.T) {
	p := mustPlugin(t, Config{
		ID:     "rt-throw",
		Source: "function onRequest(f){ header.set(f.headers,'X-Pre','1'); throw new Error('boom-42'); }",
	})
	f := newReqFlow()
	d := p.OnRequest(context.Background(), f)

	if d.Kind != flow.Continue {
		t.Fatalf("抛异常后处置 = %v,应为 Continue", d.Kind)
	}
	if got := f.Request.Header["X-Pre"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("异常前的改动应已落地,X-Pre=%v", got)
	}
	if !f.Modified {
		t.Fatal("异常前有改动时 flow 应标记为 Modified")
	}
	var msg string
	for _, e := range p.Logs() {
		if e.Level == "error" && strings.Contains(e.Msg, "boom-42") {
			msg = e.Msg
		}
	}
	if msg == "" {
		t.Fatalf("异常原文未进 error 级日志: %+v", p.Logs())
	}
}

// 验证源码初始化失败返回 error，且不产生可用实例或后台 goroutine。
func TestRuntimeInitVMRejectsBadSource(t *testing.T) {
	cases := map[string]string{
		"语法错误":    "function (",
		"表达式截断":   "var x = ",
		"顶层抛异常":   "throw new Error('top-level');",
		"顶层引用未定义": "notDefinedAnywhere();",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := NewPlugin(Config{ID: "rt-bad", Enabled: true, Source: src}, nil)
			if err == nil {
				if p != nil {
					p.Close()
				}
				t.Fatalf("源码 %q 应被拒绝", src)
			}
			if p != nil {
				p.Close()
				t.Fatalf("initVM 失败时必须返回 nil 实例,得到 %p", p)
			}
		})
	}

	const n = 20
	before := runtime.NumGoroutine()
	for i := 0; i < n; i++ {
		if _, err := NewPlugin(Config{ID: "rt-bad-leak", Enabled: true, Source: "function ("}, nil); err == nil {
			t.Fatal("语法错误源码应被拒绝")
		}
	}
	var after int
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		after = runtime.NumGoroutine()
		if after <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%d 次失败的 NewPlugin 后 goroutine 数 %d → %d,疑似每次泄漏一个", n, before, after)
}

// 验证顶层求值中断上限与单次钩子预算的独立计算规则。
func TestRuntimeInitTimeoutBounds(t *testing.T) {
	cases := []struct {
		timeout time.Duration
		want    time.Duration
	}{
		{0, time.Second},
		{time.Millisecond, time.Second},
		{20 * time.Millisecond, time.Second},
		{100 * time.Millisecond, time.Second},   // 100ms 是管理器给插件的默认值,恰好落在下限侧
		{time.Second / 10 * 2, 2 * time.Second}, // 200ms:*10 越过 1s 下限,改走线性放大
		{time.Second, 10 * time.Second},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("timeout=%v", c.timeout), func(t *testing.T) {
			if got := (&Plugin{timeout: c.timeout}).initTimeout(); got != c.want {
				t.Errorf("initTimeout() = %v,期望 %v", got, c.want)
			}
		})
	}
}

func TestRuntimeInitBudgetSeparateFromHookBudget(t *testing.T) {
	t.Parallel()
	const src = `
console.log('初始化');
function onRequest(f) { header.set(f.headers, 'X-Init', 'ok'); }
`
	// 宿主回调中的 Sleep 推进 synctest 虚拟时间，模拟顶层求值耗时。
	onLog := func(LogEntry) { time.Sleep(1100 * time.Millisecond) }

	t.Run("初始化完成后可调用钩子", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p, err := NewPlugin(Config{
				ID:      "rt-init-ok",
				Enabled: true,
				Source:  src,
				Timeout: 300 * time.Millisecond,
				OnLog:   onLog,
			}, nil)
			if err != nil {
				t.Fatalf("顶层 1.1s 在上限 3s 下应通过,却报错: %v", err)
			}
			defer p.Close()

			f := newReqFlow()
			p.OnRequest(t.Context(), f)
			if got := f.Request.Header["X-Init"]; len(got) != 1 || got[0] != "ok" {
				t.Fatalf("初始化完成后钩子应正常工作,X-Init=%v", got)
			}
		})
	})

	t.Run("初始化超时", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p, err := NewPlugin(Config{
				ID:      "rt-init-timeout",
				Enabled: true,
				Source:  src,
				Timeout: 20 * time.Millisecond,
				OnLog:   onLog,
			}, nil)
			if p != nil {
				p.Close()
				t.Fatal("顶层 1.1s 超过 1s 上限,NewPlugin 应返回 nil 实例")
			}
			if err == nil || !strings.Contains(err.Error(), "初始化超时") {
				t.Fatalf("顶层 1.1s 超过 1s 上限,期望初始化超时错误,得到 %v", err)
			}
		})
	})
}

// 验证 Close 后的请求与消息调用立即返回 Continue，并保留消息载荷。
func TestRuntimeDispatchAfterCloseFailsOpen(t *testing.T) {
	const timeout = 200 * time.Millisecond
	p, err := NewPlugin(Config{
		ID:      "rt-closed",
		Enabled: true,
		Source: `function onRequest(f){ header.set(f.headers,'X-Ok','1'); }
function onWebSocketMessage(f){ f.data = 'ws-touched'; }
function onStreamMessage(f){ f.data = 'stream-touched'; }`,
		Timeout: timeout,
	}, nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	p.Close()

	// 等待 loop 观察到 quit，确保后续调用走关闭分支。
	var (
		f       *flow.Flow
		d       flow.Decision
		elapsed time.Duration
	)
	for deadline := time.Now().Add(5 * time.Second); ; {
		f = newReqFlow()
		start := time.Now()
		d = p.OnRequest(context.Background(), f)
		elapsed = time.Since(start)
		if d.Kind == flow.Continue && !f.Modified {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("关停后 loop 始终未走 quit 分支: 处置=%v modified=%v", d.Kind, f.Modified)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if elapsed >= timeout {
		t.Fatalf("关停后调用耗时 %v,未走 quit 分支而是挂到了投递超时", elapsed)
	}
	if len(f.Request.Header["X-Ok"]) != 0 {
		t.Fatalf("已关停的插件不得再改动 flow: X-Ok=%v", f.Request.Header["X-Ok"])
	}

	const payload = "keep-me"
	m := hookWSMsg(payload)
	start := time.Now()
	if d := p.OnWebSocketMessage(context.Background(), m); d.Kind != flow.Continue {
		t.Fatalf("关停后 ws 处置 = %v,应为 Continue", d.Kind)
	}
	if wsElapsed := time.Since(start); wsElapsed >= timeout {
		t.Fatalf("关停后 ws 调用耗时 %v,未走 quit 分支", wsElapsed)
	}
	if string(m.Data) != payload {
		t.Fatalf("关停后 ws 载荷被改动: %q,期望 %q", m.Data, payload)
	}

	sm := hookStreamMsg(payload)
	start = time.Now()
	if d := p.OnStreamMessage(context.Background(), sm); d.Kind != flow.Continue {
		t.Fatalf("关停后 stream 处置 = %v,应为 Continue", d.Kind)
	}
	if stElapsed := time.Since(start); stElapsed >= timeout {
		t.Fatalf("关停后 stream 调用耗时 %v,未走 quit 分支", stElapsed)
	}
	if string(sm.Data) != payload {
		t.Fatalf("关停后 stream 载荷被改动: %q,期望 %q", sm.Data, payload)
	}
}

// 验证不可被 goja Interrupt 打断的插件仍由 dispatch 投递和 reply 超时控制。
func TestRuntimeUninterruptiblePluginFailsOpen(t *testing.T) {
	// 使用正则回溯构造不响应 Interrupt 的执行片段。
	const spin = "aaaaaaaaaaaaaaaaaaaaaa!"
	src := "function onRequest(f){ if (header.has(f.headers,'X-Spin')) { /(?=(a+)+b)/.test('" + spin + "'); } " +
		"header.set(f.headers,'X-Ok','1'); }"
	p := mustPlugin(t, Config{ID: "rt-reply", Timeout: time.Millisecond, Source: src})
	// 先完成一次普通调用，使 loop 停在 mailbox 接收点。
	p.OnRequest(context.Background(), newReqFlow())

	f := newReqFlow()
	f.Request.Header["X-Spin"] = []string{"1"}
	start := time.Now()
	d := p.OnRequest(context.Background(), f)
	elapsed := time.Since(start)

	if d.Kind != flow.Continue {
		t.Fatalf("打不断的插件调用处置 = %v,应为 Continue", d.Kind)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("调用耗时 %v,说明一直等到 VM 自己跑完,reply 超时未生效", elapsed)
	}
	if f.Modified || len(f.Request.Header["X-Ok"]) != 0 {
		t.Fatalf("被放弃的调用把改动写回了 flow: modified=%v X-Ok=%v", f.Modified, f.Request.Header["X-Ok"])
	}

	// 前一调用占用 loop 时，后续投递走 mailbox 超时分支。
	busy := newReqFlow()
	start = time.Now()
	d = p.OnRequest(context.Background(), busy)
	elapsed = time.Since(start)
	if d.Kind != flow.Continue {
		t.Fatalf("插件繁忙时处置 = %v,应为 Continue", d.Kind)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("插件繁忙时调用耗时 %v,投递超时未生效", elapsed)
	}
	if busy.Modified || len(busy.Request.Header["X-Ok"]) != 0 {
		t.Fatalf("投递失败的调用把改动写回了 flow: modified=%v X-Ok=%v", busy.Modified, busy.Request.Header["X-Ok"])
	}
}

// 验证 mailbox 串行化与 JSON 往返在并发调用间保持数据隔离。
func TestRuntimeConcurrentCallsStayIsolated(t *testing.T) {
	p := mustPlugin(t, Config{
		ID:      "rt-iso",
		Source:  "function onRequest(f){ header.set(f.headers,'X-Echo', f.url); header.set(f.headers,'X-Body', f.body); }",
		Timeout: 2 * time.Second,
	})

	const n = 16
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			f := newReqFlow()
			wantURL := fmt.Sprintf("http://example.com/u/%d", i)
			wantBody := fmt.Sprintf("body-%d", i)
			f.Request.URL = wantURL
			f.Request.Body = []byte(wantBody)
			p.OnRequest(context.Background(), f)
			if got := f.Request.Header["X-Echo"]; len(got) != 1 || got[0] != wantURL {
				done <- fmt.Errorf("第 %d 个 flow 的 X-Echo=%v,want %q", i, got, wantURL)
				return
			}
			if got := f.Request.Header["X-Body"]; len(got) != 1 || got[0] != wantBody {
				done <- fmt.Errorf("第 %d 个 flow 的 X-Body=%v,want %q", i, got, wantBody)
				return
			}
			if string(f.Request.Body) != wantBody {
				done <- fmt.Errorf("第 %d 个 flow 的 body 被改成 %q", i, f.Request.Body)
				return
			}
			done <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

// 验证 store 落盘对不可序列化值按键降级并记录路径。
func TestRuntimeFlushDropsOnlyUnserializableKey(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "state.json")

	seed := mustPlugin(t, Config{ID: "rt-store-seed", StatePath: sp, Source: "function onRequest(f){ store.set('keep','v1'); }"})
	seed.OnRequest(context.Background(), newReqFlow())
	seed.Close()
	if got := rtReadJSON(t, sp)["keep"]; got != "v1" {
		t.Fatalf("前置落盘失败,keep=%v", got)
	}

	for _, bad := range []string{"NaN", "Infinity", "-Infinity"} {
		t.Run(bad, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "rt-store-nan", StatePath: sp,
				Source: "function onRequest(f){ store.set('bad', " + bad + "); store.set('fresh','" + bad + "-ok'); }"})
			p.OnRequest(context.Background(), newReqFlow())
			p.Close()

			m := rtReadJSON(t, sp)
			if got := m["keep"]; got != "v1" {
				t.Fatalf("%s 连坐丢掉了历史键,keep=%v", bad, got)
			}
			if got := m["fresh"]; got != bad+"-ok" {
				t.Fatalf("%s 连坐丢掉了同批写入的正常键,fresh=%v", bad, got)
			}
			if _, ok := m["bad"]; ok {
				t.Fatalf("%s 被写进了磁盘: %v", bad, m["bad"])
			}
			if !rtHasErrLog(p, "store 落盘跳过了不可序列化的键", "bad") {
				t.Fatalf("丢键未留下指名坏键的 error 级日志: %+v", p.Logs())
			}
		})
	}

	// 中途取消的落盘清理临时文件。
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}

// 验证一次坏值落盘后，后续正常写入继续持久化。
func TestRuntimeFlushKeepsWorkingAfterUnserializableKey(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "state.json")
	p := mustPlugin(t, Config{ID: "rt-store-recover", StatePath: sp,
		Source: `function onRequest(f){
	  var k = header.get(f.headers,'X-Key');
	  if (k === 'bad') { store.set('bad', 0/0); } else { store.set(k, 'v-' + k); }
	}`})
	write := func(key string) {
		t.Helper()
		f := newReqFlow()
		f.Request.Header["X-Key"] = []string{key}
		if d := p.OnRequest(context.Background(), f); d.Kind != flow.Continue {
			t.Fatalf("写 %s 时处置 = %v", key, d.Kind)
		}
		p.flushStore()
	}
	write("first")
	write("bad")
	write("later")

	m := rtReadJSON(t, sp)
	if got := m["later"]; got != "v-later" {
		t.Fatalf("坏键之后的写入未落盘,later=%v(整份磁盘内容 %v)", got, m)
	}
	if got := m["first"]; got != "v-first" {
		t.Fatalf("坏键之前的写入丢失,first=%v", got)
	}
	if _, ok := m["bad"]; ok {
		t.Fatalf("坏键被写进了磁盘: %v", m["bad"])
	}
}

// 验证 Snapshot 深拷贝并按键迁移可序列化 store 值。
func TestRuntimeSnapshotDropsOnlyUnserializableKey(t *testing.T) {
	p := mustPlugin(t, Config{ID: "rt-snap-nan",
		Source: "function onRequest(f){ store.set('keep', {a:1}); store.set('bad', 0/0); store.set('n', 7); }"})
	p.OnRequest(context.Background(), newReqFlow())

	snap := p.Snapshot()
	if _, ok := snap["bad"]; ok {
		t.Fatalf("不可序列化的键出现在快照里: %v", snap["bad"])
	}
	if got := fmt.Sprint(snap["n"]); got != "7" {
		t.Fatalf("坏键连坐丢掉了正常键,n=%v(整份快照 %v)", snap["n"], snap)
	}
	nested, ok := snap["keep"].(map[string]any)
	if !ok || fmt.Sprint(nested["a"]) != "1" {
		t.Fatalf("坏键连坐丢掉了嵌套值,keep=%#v", snap["keep"])
	}
	if !rtHasErrLog(p, "store 快照跳过了不可序列化的键", "bad") {
		t.Fatalf("丢键未留下指名坏键的 error 级日志: %+v", p.Logs())
	}
}

// 验证 state.json 缺失或损坏时以空 store 启动。
func TestRuntimeLoadStoreToleratesBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
		return p
	}
	cases := map[string]string{
		"路径为空":      "",
		"文件不存在":     filepath.Join(dir, "absent.json"),
		"非法 JSON":   write("broken.json", "{ not json"),
		"JSON 数组":   write("array.json", `[1,2,3]`),
		"JSON null": write("null.json", `null`),
		"JSON 标量":   write("scalar.json", `42`),
		"空文件":       write("empty.json", ``),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			if got := loadStore(path); got != nil {
				t.Fatalf("loadStore(%q) = %v,应为 nil", path, got)
			}
			p := mustPlugin(t, Config{ID: "rt-load", StatePath: path, Source: "function onRequest(f){ store.set('fresh',1); }"})
			if snap := p.Snapshot(); len(snap) != 0 {
				t.Fatalf("坏状态文件不得带出内容: %v", snap)
			}
			if d := p.OnRequest(context.Background(), newReqFlow()); d.Kind != flow.Continue {
				t.Fatalf("处置 = %v", d.Kind)
			}
		})
	}
	if got := loadStore(write("good.json", `{"a":1}`)); fmt.Sprint(got["a"]) != "1" {
		t.Fatalf("合法状态文件未读回: %v", got)
	}
}

// 验证 StatePath 不可写时落盘记录错误且不影响插件关闭。
func TestRuntimeFlushOnUnwritablePathIsSilent(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "missing-dir", "state.json")
	p := mustPlugin(t, Config{ID: "rt-unwritable", StatePath: sp, Source: "function onRequest(f){ store.set('a',1); }"})
	p.OnRequest(context.Background(), newReqFlow())
	if got := fmt.Sprint(p.Snapshot()["a"]); got != "1" {
		t.Fatalf("内存 store 应照常工作,a=%q", got)
	}
	p.Close()
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Fatalf("不可写路径不应产生文件,stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Dir(sp)); !os.IsNotExist(err) {
		t.Fatalf("落盘不得自建目录,stat err=%v", err)
	}
}

// 验证后台定时刷盘按固定 2s 周期写入 store。
func TestRuntimePeriodicFlushWithoutClose(t *testing.T) {
	if testing.Short() {
		t.Skip("需真实等待 storeFlusher 的 2s ticker")
	}
	t.Parallel()
	sp := filepath.Join(t.TempDir(), "state.json")
	p := mustPlugin(t, Config{ID: "rt-flusher", StatePath: sp, Source: "function onRequest(f){ store.set('ticked','yes'); }"})
	p.OnRequest(context.Background(), newReqFlow())

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sp); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("超过 10s 仍未定时落盘,storeFlusher 未生效")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := rtReadJSON(t, sp)["ticked"]; got != "yes" {
		t.Fatalf("定时落盘内容 = %v,want \"yes\"", got)
	}
}

// 验证 Snapshot 返回深拷贝，调用方修改结果不影响内部 store。
func TestRuntimeSnapshotIsDeepCopy(t *testing.T) {
	src := `function onRequest(f){
	  if (!store.get('m')) { store.set('m', {a:1}); }
	  header.set(f.headers,'X-A', String(json.get(store.get('m'),'a')));
	  header.set(f.headers,'X-Injected', String(store.get('injected')));
	}`
	p := mustPlugin(t, Config{ID: "rt-snap", Source: src})
	p.OnRequest(context.Background(), newReqFlow())

	snap := p.Snapshot()
	nested, ok := snap["m"].(map[string]any)
	if !ok {
		t.Fatalf("Snapshot 里 m 的类型 = %T", snap["m"])
	}
	nested["a"] = 99
	snap["injected"] = "leak"
	delete(snap, "m")

	f := newReqFlow()
	p.OnRequest(context.Background(), f)
	if got := f.Request.Header["X-A"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("改动 Snapshot 回灌了插件内部嵌套值,X-A=%v", got)
	}
	if got := f.Request.Header["X-Injected"]; len(got) != 1 || got[0] != "null" {
		t.Fatalf("Snapshot 上新增的键漏进了插件内部 store,X-Injected=%v", got)
	}
	if _, still := p.Snapshot()["m"]; !still {
		t.Fatal("从 Snapshot 删键影响到了插件内部 store")
	}
}

// 验证 Close 可重复调用且保持安全。
func TestRuntimeCloseIsIdempotent(t *testing.T) {
	for _, withState := range []bool{false, true} {
		t.Run(fmt.Sprintf("statePath=%v", withState), func(t *testing.T) {
			cfg := Config{ID: "rt-close", Enabled: true, Source: "function onRequest(f){ store.set('k','v'); }"}
			if withState {
				cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
			}
			p, err := NewPlugin(cfg, nil)
			if err != nil {
				t.Fatalf("NewPlugin: %v", err)
			}
			p.OnRequest(context.Background(), newReqFlow())
			p.Close()
			p.Close()

			done := make(chan struct{}, 4)
			for i := 0; i < 4; i++ {
				go func() { p.Close(); done <- struct{}{} }()
			}
			for i := 0; i < 4; i++ {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("并发 Close 卡死")
				}
			}
			if withState {
				if got := rtReadJSON(t, cfg.StatePath)["k"]; got != "v" {
					t.Fatalf("Close 应完成最终落盘,k=%v", got)
				}
			}
		})
	}
}

// 验证共用 StatePath 的实例使用独立临时文件完成原子替换。
func TestRuntimeTmpFileNoncePerInstance(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "state.json")
	a := mustPlugin(t, Config{ID: "rt-nonce-a", StatePath: sp, Source: "function onRequest(f){ store.set('who','a'); store.set('pad', crypto.randomString(4096)); }"})
	b := mustPlugin(t, Config{ID: "rt-nonce-b", StatePath: sp, Source: "function onRequest(f){ store.set('who','b'); store.set('pad', crypto.randomString(4096)); }"})

	if a.tmpNonce == "" || b.tmpNonce == "" {
		t.Fatalf("StatePath 非空时 tmpNonce 不应为空: a=%q b=%q", a.tmpNonce, b.tmpNonce)
	}
	if a.tmpNonce == b.tmpNonce {
		t.Fatalf("两个实例共用临时文件名 %q", a.tmpNonce)
	}

	a.OnRequest(context.Background(), newReqFlow())
	b.OnRequest(context.Background(), newReqFlow())

	done := make(chan struct{}, 2)
	for _, p := range []*Plugin{a, b} {
		go func(p *Plugin) {
			for i := 0; i < 100; i++ {
				p.flushStore()
			}
			done <- struct{}{}
		}(p)
	}
	<-done
	<-done

	// 并发落盘后的目标文件应为某一个实例的完整快照。
	m := rtReadJSON(t, sp)
	who, _ := m["who"].(string)
	if who != "a" && who != "b" {
		t.Fatalf("并发落盘后 who=%v,文件内容已被交错破坏: %v", m["who"], m)
	}
	if pad, _ := m["pad"].(string); len(pad) != 4096 {
		t.Fatalf("落盘内容被截断,pad 长度 = %d", len(pad))
	}
}

// rtSpyLogger 记录宿主 Logger 收到的 Debug 调用(按 Logger 自己的 printf 语义展开)。
type rtSpyLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *rtSpyLogger) Debug(msg string, args ...any) {
	l.mu.Lock()
	l.msgs = append(l.msgs, fmt.Sprintf(msg, args...))
	l.mu.Unlock()
}
func (l *rtSpyLogger) Info(string, ...any)  {}
func (l *rtSpyLogger) Error(string, ...any) {}

func (l *rtSpyLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.msgs...)
}

// 验证 console 日志同时写入插件缓冲与宿主 Logger，并包含插件 ID。
func TestRuntimeConsoleLogReachesHostLogger(t *testing.T) {
	spy := &rtSpyLogger{}
	p, err := NewPlugin(Config{ID: "rt-logger", Enabled: true,
		Source: "function onRequest(f){ console.log('hello', {a:1}); }"}, spy)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	t.Cleanup(p.Close)
	p.OnRequest(context.Background(), newReqFlow())

	msgs := spy.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("宿主 Logger 收到 %d 条 Debug 调用,期望 1 条: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "rt-logger") || !strings.Contains(msgs[0], "hello") ||
		!strings.Contains(msgs[0], `{"a":1}`) {
		t.Fatalf("宿主日志未带上插件 ID 或消息原文: %q", msgs[0])
	}

	logs := p.Logs()
	if len(logs) != 1 || logs[0].Level != "log" || !strings.Contains(logs[0].Msg, "hello") {
		t.Fatalf("同一条 console.log 未进插件日志缓冲: %+v", logs)
	}
}

// 验证延迟调度的超时回调按运行代次识别并忽略过期回调。
func TestRuntimeStaleTimeoutCallbackCannotInterruptNextRun(t *testing.T) {
	p := mustPlugin(t, Config{ID: "rt-stale-cb",
		Source: "function onRequest(f){ header.set(f.headers,'X-Ok','1'); }"})

	stale := p.beginRun() // 上一次运行领到的代次
	p.endRun()            // 它已经正常结束并清了中断标志
	p.beginRun()          // 下一次运行开始
	p.timeoutInterrupt(stale)

	f := newReqFlow()
	if d := p.OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
	}
	if got := f.Request.Header["X-Ok"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("钩子未跑完,X-Ok=%v:上一次运行的中断标志泄漏到了这次调用", got)
	}
}

// 验证循环引用在环处跳过，并保留同一键中的可序列化部分。
func TestRuntimeCyclicStoreValueStopsAtCycle(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "state.json")
	p := mustPlugin(t, Config{ID: "rt-store-cycle", StatePath: sp,
		Source: "function onRequest(f){ store.set('keep','v1'); var a={n:1}; a.self=a; store.set('cyc', a); }"})
	p.OnRequest(context.Background(), newReqFlow())
	p.Close()

	m := rtReadJSON(t, sp)
	if got := m["keep"]; got != "v1" {
		t.Fatalf("循环引用连坐丢掉了正常键,keep=%v", got)
	}
	cyc, ok := m["cyc"].(map[string]any)
	if !ok || fmt.Sprint(cyc["n"]) != "1" {
		t.Fatalf("环外的字段应照常保留,cyc=%#v", m["cyc"])
	}
	if _, ok := cyc["self"]; ok {
		t.Fatalf("环被展开落盘了,cyc=%#v", cyc)
	}
	if !rtHasErrLog(p, "store 落盘跳过了不可序列化的键", "cyc.self") {
		t.Fatalf("丢弃环未留下指名路径的 error 级日志: %+v", p.Logs())
	}
}

// 验证坏值降级后仅记录一次告警，后续落盘使用正常路径。
func TestRuntimeFlushDropWarningDoesNotRepeat(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "state.json")
	p := mustPlugin(t, Config{ID: "rt-store-warn-once", StatePath: sp,
		Source: "function onRequest(f){ store.set('bad', 0/0); store.set('ok','v'); }"})
	p.OnRequest(context.Background(), newReqFlow())

	count := func() int {
		n := 0
		for _, e := range p.Logs() {
			if e.Level == "error" && strings.Contains(e.Msg, "store 落盘跳过了不可序列化的键") {
				n++
			}
		}
		return n
	}
	p.flushStore()
	if got := count(); got != 1 {
		t.Fatalf("首次落盘的告警数 = %d, 期望 1", got)
	}
	p.flushStore()
	p.flushStore()
	if got := count(); got != 1 {
		t.Fatalf("重复落盘后的告警数 = %d, 期望仍是 1(坏值应已从内存 store 里消失)", got)
	}
	if got := rtReadJSON(t, sp)["ok"]; got != "v" {
		t.Fatalf("坏值收敛后正常键丢了,ok=%v", got)
	}
}
