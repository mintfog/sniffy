// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package js

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

func hookWSMsg(data string) *flow.WSMessage {
	return &flow.WSMessage{
		FlowID:    "ws-flow",
		URL:       "wss://example.com/socket",
		Direction: flow.WSClientToServer,
		Type:      flow.WSText,
		Data:      []byte(data),
	}
}

func hookStreamMsg(data string) *flow.StreamMessage {
	return &flow.StreamMessage{
		FlowID:    "st-flow",
		URL:       "https://example.com/events",
		Direction: flow.WSServerToClient,
		Kind:      flow.StreamSSE,
		EventType: "tick",
		Data:      []byte(data),
	}
}

// hookWSBytesMsg / hookStreamBytesMsg 构造带指定帧类型和任意字节载荷的消息帧。
func hookWSBytesMsg(typ string, data []byte) *flow.WSMessage {
	m := hookWSMsg("")
	m.Type = typ
	m.Data = data
	return m
}

func hookStreamBytesMsg(kind string, data []byte) *flow.StreamMessage {
	m := hookStreamMsg("")
	m.Kind = kind
	m.Data = data
	return m
}

// hookRunMessage 执行指定消息钩子并返回处置与回写载荷。
func hookRunMessage(p *Plugin, hook, data string) (flow.Decision, string) {
	if hook == "onWebSocketMessage" {
		m := hookWSMsg(data)
		return p.OnWebSocketMessage(context.Background(), m), string(m.Data)
	}
	m := hookStreamMsg(data)
	return p.OnStreamMessage(context.Background(), m), string(m.Data)
}

func hookHasLog(p *Plugin, level, needle string) bool {
	for _, e := range p.Logs() {
		if e.Level == level && strings.Contains(e.Msg, needle) {
			return true
		}
	}
	return false
}

// 验证插件 URL 匹配模式：星号仅在首尾表示通配，中间位置按字面比较。
func TestHookMatchPatternForms(t *testing.T) {
	const target = "https://api.example.com/v1/users"
	cases := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"单星全放行", "*", true},
		{"空串全放行", "", true},
		{"双星去星后为空串,Contains 恒真", "**", true},
		{"前缀命中", "https://api.*", true},
		{"前缀不命中", "https://cdn.*", false},
		{"后缀命中", "*/users", true},
		{"后缀不命中", "*/orders", false},
		{"前后带星走 Contains 命中", "*example.com*", true},
		{"前后带星走 Contains 不命中", "*example.org*", false},
		{"精确命中", target, true},
		{"精确比较不做前缀放宽", "https://api.example.com/v1", false},
		{"中间星号不作通配", "https://api*users", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchPattern(c.pattern, target); got != c.want {
				t.Fatalf("matchPattern(%q, %q) = %v, 期望 %v", c.pattern, target, got, c.want)
			}
		})
	}
}

// 验证白名单与黑名单的匹配优先级及空白名单语义。
func TestHookMatchListPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		whitelist []string
		blacklist []string
		url       string
		want      bool
	}{
		{"白名单为空即全放行", nil, nil, "https://a.com/x", true},
		{"黑白同时命中时黑名单胜出", []string{"*a.com*"}, []string{"*/private*"}, "https://a.com/private/x", false},
		{"白名单命中且黑名单不命中", []string{"*a.com*"}, []string{"*/private*"}, "https://a.com/pub/x", true},
		{"白名单非空且不命中", []string{"*b.com*"}, nil, "https://a.com/x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "match", Whitelist: c.whitelist, Blacklist: c.blacklist,
				Source: "function onRequest(f){}"})
			if got := p.Match(c.url); got != c.want {
				t.Fatalf("Match(%q) = %v, 期望 %v (白名单=%v 黑名单=%v)", c.url, got, c.want, c.whitelist, c.blacklist)
			}
		})
	}
}

// 验证插件身份、优先级及启用状态的公开接口。
func TestHookIdentitySurface(t *testing.T) {
	p := mustPlugin(t, Config{ID: "id-1", Name: "友好展示名", Priority: 7, Source: "function onRequest(f){}"})
	if p.Name() != "id-1" {
		t.Fatalf("Name() = %q, 期望 cfg.ID %q", p.Name(), "id-1")
	}
	if p.Priority() != 7 {
		t.Fatalf("Priority() = %d, 期望 7", p.Priority())
	}
	if !p.Enabled() {
		t.Fatal("新建插件的 Enabled() = false, 期望 true")
	}
	p.SetEnabled(false)
	if p.Enabled() {
		t.Fatal("SetEnabled(false) 后 Enabled() 仍为 true")
	}
	p.SetEnabled(true)
	if !p.Enabled() {
		t.Fatal("SetEnabled(true) 后 Enabled() 仍为 false")
	}

	// manifest 的 enabled=false 使插件以停用态创建。
	off, err := NewPlugin(Config{ID: "id-2", Source: "function onRequest(f){}"}, nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	t.Cleanup(off.Close)
	if off.Enabled() {
		t.Fatal("cfg.Enabled=false 的插件出厂即为启用态")
	}
}

// 验证 Enabled 与 SetEnabled 支持并发访问。
func TestHookSetEnabledConcurrentAccess(t *testing.T) {
	p := mustPlugin(t, Config{ID: "toggle", Source: "function onRequest(f){}"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				p.SetEnabled(n%2 == 0)
			}
		}(i)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_ = p.Enabled()
			}
		}()
	}
	wg.Wait()
	p.SetEnabled(false)
	if p.Enabled() {
		t.Fatal("并发读写后 SetEnabled(false) 未生效")
	}
}

// 验证 WebSocket 钩子的方向、类型、URL、载荷字段及回写。
func TestHookWSExposesFieldsAndWritesBack(t *testing.T) {
	src := `function onWebSocketMessage(f){ f.data = [f.direction, f.type, f.url, f.data].join('|'); }`
	p := mustPlugin(t, Config{ID: "ws-fields", Source: src})
	m := hookWSMsg("payload")
	d := p.OnWebSocketMessage(context.Background(), m)
	if d.Kind != flow.Continue {
		t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
	}
	want := "client->server|text|wss://example.com/socket|payload"
	if string(m.Data) != want {
		t.Fatalf("m.Data = %q, 期望 %q", m.Data, want)
	}
}

// 验证流消息钩子的方向、kind、eventType、URL、载荷字段及回写。
func TestHookStreamExposesFieldsAndWritesBack(t *testing.T) {
	src := `function onStreamMessage(f){ f.data = [f.direction, f.kind, f.eventType, f.url, f.data].join('|'); }`
	p := mustPlugin(t, Config{ID: "st-fields", Source: src})
	m := hookStreamMsg("payload")
	d := p.OnStreamMessage(context.Background(), m)
	if d.Kind != flow.Continue {
		t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
	}
	want := "server->client|sse|tick|https://example.com/events|payload"
	if string(m.Data) != want {
		t.Fatalf("m.Data = %q, 期望 %q", m.Data, want)
	}
}

// 验证消息阶段的 mock()/setBreakpoint() 记录 warn、返回 Continue，并继续执行脚本。
func TestHookMockAndBreakpointRejectedInMessagePhases(t *testing.T) {
	cases := []struct {
		name  string
		hook  string
		phase string
		api   string
		call  string
	}{
		{"ws-mock", "onWebSocketMessage", "ws", "mock()", "mock({status:200, body:'x'})"},
		{"ws-breakpoint", "onWebSocketMessage", "ws", "setBreakpoint()", "setBreakpoint()"},
		{"stream-mock", "onStreamMessage", "stream", "mock()", "mock({status:200, body:'x'})"},
		{"stream-breakpoint", "onStreamMessage", "stream", "setBreakpoint()", "setBreakpoint()"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := fmt.Sprintf("function %s(f){ %s; f.data='tail-ran'; }", c.hook, c.call)
			p := mustPlugin(t, Config{ID: c.name, Source: src})
			d, data := hookRunMessage(p, c.hook, "orig")
			if d.Kind != flow.Continue {
				t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
			}
			if !hookHasLog(p, "warn", c.api) || !hookHasLog(p, "warn", c.phase) {
				t.Fatalf("缺少指明 %s 与阶段 %s 的 warn 日志: %+v", c.api, c.phase, p.Logs())
			}
			if data != "tail-ran" {
				t.Fatalf("被拒绝的 API 中断了脚本余下逻辑: data = %q", data)
			}
		})
	}
}

// 验证消息阶段 abort() 返回 Abort 及 status/reason。
func TestHookAbortInMessagePhases(t *testing.T) {
	cases := []struct {
		name       string
		hook       string
		call       string
		wantStatus int
		wantReason string
	}{
		{"ws-带状态", "onWebSocketMessage", "abort({status:403, reason:'blocked'})", 403, "blocked"},
		{"ws-无参即直接断开", "onWebSocketMessage", "abort()", 0, ""},
		{"stream-带状态", "onStreamMessage", "abort({status:502, reason:'bad upstream'})", 502, "bad upstream"},
		{"stream-无参即直接断开", "onStreamMessage", "abort()", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := fmt.Sprintf("function %s(f){ %s; }", c.hook, c.call)
			p := mustPlugin(t, Config{ID: c.name, Source: src})
			d, _ := hookRunMessage(p, c.hook, "orig")
			if d.Kind != flow.Abort {
				t.Fatalf("处置 = %v, 期望 Abort", d.Kind)
			}
			if d.StatusOnAbort != c.wantStatus || d.Reason != c.wantReason {
				t.Fatalf("status=%d reason=%q, 期望 status=%d reason=%q",
					d.StatusOnAbort, d.Reason, c.wantStatus, c.wantReason)
			}
		})
	}
}

// 验证消息钩子脚本异常时返回 Continue、保留载荷并记录 error 日志。
func TestHookMessageHookFailsOpenOnScriptError(t *testing.T) {
	for _, hook := range []string{"onWebSocketMessage", "onStreamMessage"} {
		t.Run(hook, func(t *testing.T) {
			src := fmt.Sprintf("function %s(f){ throw new Error('boom'); }", hook)
			p := mustPlugin(t, Config{ID: "err-" + hook, Source: src})
			d, data := hookRunMessage(p, hook, "orig")
			if d.Kind != flow.Continue {
				t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
			}
			if data != "orig" {
				t.Fatalf("异常后载荷被破坏: %q, 期望 %q", data, "orig")
			}
			if !hookHasLog(p, "error", "boom") {
				t.Fatalf("异常未留痕: %+v", p.Logs())
			}
		})
	}
}

// 验证未定义消息钩子经过 JSON 往返后保持文本和二进制载荷字节一致。
func TestHookUndefinedMessageHookKeepsPayloadIntact(t *testing.T) {
	cases := []struct {
		name    string
		wsType  string
		kind    string
		payload []byte
	}{
		{"utf8-含 NUL 与控制字符与多字节", flow.WSText, flow.StreamSSE, []byte("中文\x00\x01\t\"\\ tail é")},
		{"二进制帧", flow.WSBinary, flow.StreamChunk, []byte{0x00, 0x01, 0xff, 0xfe, 0x80, 0x61}},
		{"grpc 帧", flow.WSBinary, flow.StreamGRPC, []byte{0x08, 0x96, 0x01, 0xff}},
		{"0x00-0xff 全字节", flow.WSBinary, flow.StreamGRPC, convAllBytes()},
	}
	p := mustPlugin(t, Config{ID: "http-only", Source: "function onRequest(f){}"})
	for _, c := range cases {
		t.Run("ws/"+c.name, func(t *testing.T) {
			m := hookWSBytesMsg(c.wsType, append([]byte(nil), c.payload...))
			if d := p.OnWebSocketMessage(context.Background(), m); d.Kind != flow.Continue {
				t.Fatalf("ws 处置 = %v, 期望 Continue", d.Kind)
			}
			if !bytes.Equal(m.Data, c.payload) {
				t.Fatalf("ws 载荷被改动: %x, 期望 %x", m.Data, c.payload)
			}
		})
		t.Run("stream/"+c.name, func(t *testing.T) {
			s := hookStreamBytesMsg(c.kind, append([]byte(nil), c.payload...))
			if d := p.OnStreamMessage(context.Background(), s); d.Kind != flow.Continue {
				t.Fatalf("stream 处置 = %v, 期望 Continue", d.Kind)
			}
			if !bytes.Equal(s.Data, c.payload) {
				t.Fatalf("stream 载荷被改动: %x, 期望 %x", s.Data, c.payload)
			}
		})
	}
}

// 验证并发消息钩子通过邮箱串行访问 VM，连接之间保持数据隔离。
func TestHookConcurrentMessageHooksDoNotCrossTalk(t *testing.T) {
	p := mustPlugin(t, Config{ID: "conc", Timeout: 2 * time.Second,
		Source: `function onWebSocketMessage(f){ f.data = f.data + '!'; }
function onStreamMessage(f){ f.data = f.data + '?'; }`})

	var wg sync.WaitGroup
	errs := make(chan string, 128)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for n := 0; n < 5; n++ {
				tag := fmt.Sprintf("g%d-n%d", g, n)
				m := hookWSMsg(tag)
				p.OnWebSocketMessage(context.Background(), m)
				if string(m.Data) != tag+"!" {
					errs <- fmt.Sprintf("ws: 实际 %q, 期望 %q", m.Data, tag+"!")
				}
				s := hookStreamMsg(tag)
				p.OnStreamMessage(context.Background(), s)
				if string(s.Data) != tag+"?" {
					errs <- fmt.Sprintf("stream: 实际 %q, 期望 %q", s.Data, tag+"?")
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// 验证日志缓冲保留最新的 200 条记录。
func TestHookLogRingBufferKeepsNewest(t *testing.T) {
	p := mustPlugin(t, Config{ID: "ring", Timeout: 5 * time.Second,
		Source: `function onRequest(f){ for (var i=0;i<250;i++) console.log('n'+i); }`})
	p.OnRequest(context.Background(), newReqFlow())

	logs := p.Logs()
	if len(logs) != 200 {
		t.Fatalf("len(Logs()) = %d, 期望 200", len(logs))
	}
	if logs[0].Msg != "n50" {
		t.Fatalf("最旧一条 = %q, 期望 %q(应丢掉前 50 条)", logs[0].Msg, "n50")
	}
	if logs[len(logs)-1].Msg != "n249" {
		t.Fatalf("最新一条 = %q, 期望 %q", logs[len(logs)-1].Msg, "n249")
	}
}

// 验证 ClearLogs 后日志缓冲仍可接收新记录。
func TestHookClearLogsThenKeepsAppending(t *testing.T) {
	p := mustPlugin(t, Config{ID: "clear", Source: `function onRequest(f){ console.log('tick'); }`})
	p.OnRequest(context.Background(), newReqFlow())
	if len(p.Logs()) == 0 {
		t.Fatal("首次调用未产生日志")
	}

	p.ClearLogs()
	if got := p.Logs(); len(got) != 0 {
		t.Fatalf("ClearLogs 后 Logs() = %+v, 期望为空", got)
	}

	p.OnRequest(context.Background(), newReqFlow())
	logs := p.Logs()
	if len(logs) != 1 || logs[0].Msg != "tick" {
		t.Fatalf("清空后新日志未正常追加: %+v", logs)
	}
}

// 验证 OnLog 为每条记录推送与 Logs() 一致的内容。
func TestHookOnLogCallbackSeesEveryEntry(t *testing.T) {
	var mu sync.Mutex
	var pushed []LogEntry
	p := mustPlugin(t, Config{ID: "onlog", Timeout: 5 * time.Second,
		OnLog: func(e LogEntry) {
			mu.Lock()
			pushed = append(pushed, e)
			mu.Unlock()
		},
		Source: `function onRequest(f){ for (var i=0;i<250;i++) console.info('m'+i); }`})
	p.OnRequest(context.Background(), newReqFlow())

	mu.Lock()
	defer mu.Unlock()
	if len(pushed) != 250 {
		t.Fatalf("OnLog 回调 %d 次, 期望 250(裁剪只影响内存快照,不影响推送)", len(pushed))
	}
	logs := p.Logs()
	tail := pushed[len(pushed)-len(logs):]
	for i := range logs {
		if logs[i] != tail[i] {
			t.Fatalf("第 %d 条: Logs()=%+v, OnLog=%+v", i, logs[i], tail[i])
		}
	}
	if pushed[0].Level != "info" || pushed[0].Msg != "m0" {
		t.Fatalf("首条推送 = %+v, 期望 level=info msg=m0", pushed[0])
	}
}

// 验证通过 dataB64 通道改写 WebSocket 与流消息的二进制载荷。
func TestHookMessagePayloadRewrittenViaB64(t *testing.T) {
	t.Run("ws", func(t *testing.T) {
		src := `function onWebSocketMessage(f){
		  var b = base64.decodeBytes(f.dataB64);
		  b[0] = 2; b.push(0);
		  f.dataB64 = base64.encodeBytes(b);
		}`
		p := mustPlugin(t, Config{ID: "ws-b64-edit", Source: src})
		m := hookWSBytesMsg(flow.WSBinary, []byte{0x00, 0x01, 0xff, 0xfe, 0x80, 0x61})
		if d := p.OnWebSocketMessage(context.Background(), m); d.Kind != flow.Continue {
			t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
		}
		want := []byte{0x02, 0x01, 0xff, 0xfe, 0x80, 0x61, 0x00}
		if !bytes.Equal(m.Data, want) {
			t.Fatalf("ws 载荷 = %x, 期望 %x", m.Data, want)
		}
	})
	t.Run("stream", func(t *testing.T) {
		src := `function onStreamMessage(f){
		  var b = base64.decodeBytes(f.dataB64);
		  b[3] = 254;
		  f.dataB64 = base64.encodeBytes(b);
		}`
		p := mustPlugin(t, Config{ID: "st-b64-edit", Source: src})
		s := hookStreamBytesMsg(flow.StreamGRPC, []byte{0x08, 0x96, 0x01, 0xff})
		if d := p.OnStreamMessage(context.Background(), s); d.Kind != flow.Continue {
			t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
		}
		want := []byte{0x08, 0x96, 0x01, 0xfe}
		if !bytes.Equal(s.Data, want) {
			t.Fatalf("stream 载荷 = %x, 期望 %x", s.Data, want)
		}
	})
}

// 验证消息载荷通道按 UTF-8 合法性选择，并遵循文本字段优先级。
func TestHookMessageChannelSelection(t *testing.T) {
	src := `function onWebSocketMessage(f){ f.data = 'b64=' + (typeof f.dataB64) + ' text=' + JSON.stringify(f.data); }`
	cases := []struct {
		name    string
		payload []byte
		want    string
	}{
		{"合法 utf8 的二进制帧", []byte("你好"), `b64=undefined text="你好"`},
		{"非 utf8 载荷", []byte{0xff, 0xfe, 0x80}, `b64=string text=""`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "ws-channel", Source: src})
			m := hookWSBytesMsg(flow.WSBinary, append([]byte(nil), c.payload...))
			p.OnWebSocketMessage(context.Background(), m)
			if string(m.Data) != c.want {
				t.Fatalf("ws 载荷 = %q, 期望 %q", m.Data, c.want)
			}
		})
	}
}

// 验证 dataB64 解码失败时保留原载荷并记录日志。
func TestHookMessageUndecodableB64KeepsPayload(t *testing.T) {
	orig := []byte{0x00, 0xff, 0xfe, 0x41, 0x80}
	t.Run("ws", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "ws-bad-b64",
			Source: "function onWebSocketMessage(f){ f.dataB64 = '!!!'; }"})
		m := hookWSBytesMsg(flow.WSBinary, append([]byte(nil), orig...))
		p.OnWebSocketMessage(context.Background(), m)
		if !bytes.Equal(m.Data, orig) {
			t.Fatalf("ws 载荷 = %x, 期望保持 %x", m.Data, orig)
		}
		if !hookHasLog(p, "error", "dataB64") {
			t.Fatalf("解码失败未留痕: %+v", p.Logs())
		}
	})
	t.Run("stream", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "st-bad-b64",
			Source: "function onStreamMessage(f){ f.dataB64 = '!!!'; }"})
		s := hookStreamBytesMsg(flow.StreamGRPC, append([]byte(nil), orig...))
		p.OnStreamMessage(context.Background(), s)
		if !bytes.Equal(s.Data, orig) {
			t.Fatalf("stream 载荷 = %x, 期望保持 %x", s.Data, orig)
		}
		if !hookHasLog(p, "error", "dataB64") {
			t.Fatalf("解码失败未留痕: %+v", p.Logs())
		}
	})
}

// 验证消息钩子保持 nil 与空切片载荷的区分。
func TestHookEmptyMessagePayloadNotReshaped(t *testing.T) {
	p := mustPlugin(t, Config{ID: "empty-msg",
		Source: "function onWebSocketMessage(f){} function onStreamMessage(f){}"})

	m := hookWSBytesMsg(flow.WSBinary, nil)
	p.OnWebSocketMessage(context.Background(), m)
	if m.Data != nil {
		t.Fatalf("nil ws 载荷被换成了 %#v", m.Data)
	}

	m2 := hookWSBytesMsg(flow.WSText, []byte{})
	p.OnWebSocketMessage(context.Background(), m2)
	if m2.Data == nil || len(m2.Data) != 0 {
		t.Fatalf("空 ws 载荷被换成了 %#v", m2.Data)
	}

	s := hookStreamBytesMsg(flow.StreamGRPC, nil)
	p.OnStreamMessage(context.Background(), s)
	if s.Data != nil {
		t.Fatalf("nil stream 载荷被换成了 %#v", s.Data)
	}
}

// 验证文本消息上的 dataB64 冲突记录及显式切换规则。
func TestHookMessageB64WriteOnTextPayload(t *testing.T) {
	p := mustPlugin(t, Config{ID: "ws-b64-on-text",
		Source: "function onWebSocketMessage(f){ f.dataB64 = base64.encodeBytes([0,255]); }"})
	m := hookWSBytesMsg(flow.WSText, []byte("plain"))
	p.OnWebSocketMessage(context.Background(), m)
	if string(m.Data) != "plain" {
		t.Fatalf("ws 载荷 = %x, 期望保持 plain", m.Data)
	}
	if !hookHasLog(p, "error", "同时有值") {
		t.Fatalf("未留痕: %+v", p.Logs())
	}
}
