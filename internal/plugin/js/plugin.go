// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package js 用 goja 实现可由用户编写的 JavaScript 插件层。
//
// 设计要点(v1):
//   - 每个插件独占一个 goja.Runtime,由一个邮箱 goroutine 串行执行(goja 非协程安全)。
//   - flow 以 JSON 进出 VM(JSON.parse/stringify),避免宿主对象绑定的边界陷阱,简单且正确。
//   - 每次调用设超时,到点 Interrupt;脚本报错/超时一律失败开放(Continue),绝不影响代理。
//   - 暴露给脚本的 API:onRequest/onResponse/onWebSocketMessage、mock()/abort()/setBreakpoint()、
//     console.*、store.get/set、settings。
package js

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/mintfog/sniffy/internal/flow"
)

// Logger 是 js 插件需要的最小日志接口。
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
}

// Config 创建一个 JS 插件所需的元信息。
type Config struct {
	ID        string
	Name      string
	Priority  int
	Enabled   bool
	Whitelist []string
	Blacklist []string
	Settings  map[string]any
	Source    string
	Timeout   time.Duration
}

// Plugin 是一个 goja JS 插件,实现 pipeline 的钩子接口。
type Plugin struct {
	cfg     Config
	enabled atomic.Bool
	timeout time.Duration

	vm      *goja.Runtime
	driver  *goja.Program
	mailbox chan *job
	quit    chan struct{}
	once    sync.Once

	logger  Logger
	store   map[string]any
	storeMu sync.Mutex

	logsMu sync.Mutex
	logs   []string
}

type job struct {
	phase string
	in    []byte
	reply chan []byte
}

// 进出 VM 的 flow 视图(请求字段扁平到顶层,响应在 response 下)。
type jsFlow struct {
	ID       string            `json:"id"`
	Method   string            `json:"method,omitempty"`
	URL      string            `json:"url,omitempty"`
	Host     string            `json:"host,omitempty"`
	Path     string            `json:"path,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Response *jsResponse       `json:"response,omitempty"`
	Process  *jsProcess        `json:"process,omitempty"`

	// WS / 流(SSE / gRPC / 分块)专用字段。
	Direction string `json:"direction,omitempty"`
	Type      string `json:"type,omitempty"`
	Data      string `json:"data,omitempty"`
	Kind      string `json:"kind,omitempty"`      // 流类型:sse|grpc|chunk
	EventType string `json:"eventType,omitempty"` // SSE 的 event 名
}

type jsResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Reason     string            `json:"reason,omitempty"`
}

type jsProcess struct {
	Name string `json:"name,omitempty"`
	PID  uint32 `json:"pid,omitempty"`
	Path string `json:"path,omitempty"`
}

type jsDecision struct {
	Kind   string `json:"kind"`
	Status int    `json:"status"`
	Reason string `json:"reason"`
}

type jsOut struct {
	Flow     jsFlow     `json:"flow"`
	Decision jsDecision `json:"decision"`
}

const hostSetup = `
var flow; var __decision;
var __IN__; var __PHASE__; var __OUT__;
var __STOP = {__stop:true};
var console = {
  log:   function(){ __log('log',   Array.prototype.join.call(arguments,' ')); },
  info:  function(){ __log('info',  Array.prototype.join.call(arguments,' ')); },
  warn:  function(){ __log('warn',  Array.prototype.join.call(arguments,' ')); },
  error: function(){ __log('error', Array.prototype.join.call(arguments,' ')); }
};
var store = { get: function(k){ return __storeGet(k); }, set: function(k,v){ __storeSet(k,v); } };
function notify(t,m){ __notify(t||'', m||''); }
function mock(r){ if(r){ flow.response = r; } __decision={kind:'mock', status:0, reason:(r&&r.reason)||''}; throw __STOP; }
function abort(o){ o=o||{}; __decision={kind:'abort', status:(o.status||0), reason:(o.reason||'')}; throw __STOP; }
function setBreakpoint(){ __decision={kind:'breakpoint', status:0, reason:''}; throw __STOP; }
`

const driverSrc = `
(function(){
  flow = JSON.parse(__IN__);
  __decision = {kind:'continue', status:0, reason:''};
  try {
    if (__PHASE__ === 'request') { if (typeof onRequest === 'function') onRequest(flow); }
    else if (__PHASE__ === 'response') { if (typeof onResponse === 'function') onResponse(flow); }
    else if (__PHASE__ === 'ws') { if (typeof onWebSocketMessage === 'function') onWebSocketMessage(flow); }
    else if (__PHASE__ === 'stream') { if (typeof onStreamMessage === 'function') onStreamMessage(flow); }
  } catch (e) { if (e !== __STOP) { __log('error', 'plugin error: ' + e); } }
  __OUT__ = JSON.stringify({flow: flow, decision: __decision});
})();
`

// NewPlugin 创建并启动一个 JS 插件。
func NewPlugin(cfg Config, logger Logger) (*Plugin, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 100 * time.Millisecond
	}
	p := &Plugin{
		cfg:     cfg,
		timeout: cfg.Timeout,
		mailbox: make(chan *job),
		quit:    make(chan struct{}),
		logger:  logger,
		store:   make(map[string]any),
	}
	p.enabled.Store(cfg.Enabled)
	if err := p.initVM(); err != nil {
		return nil, err
	}
	go p.loop()
	return p, nil
}

func (p *Plugin) initVM() error {
	vm := goja.New()
	vm.SetMaxCallStackSize(2048)

	_ = vm.Set("__log", func(level, msg string) {
		p.appendLog(level + ": " + msg)
		if p.logger != nil {
			p.logger.Debug("[插件:%s] %s", p.cfg.ID, msg)
		}
	})
	_ = vm.Set("__storeGet", func(k string) any {
		p.storeMu.Lock()
		defer p.storeMu.Unlock()
		return p.store[k]
	})
	_ = vm.Set("__storeSet", func(k string, v any) {
		p.storeMu.Lock()
		defer p.storeMu.Unlock()
		p.store[k] = v
	})
	_ = vm.Set("__notify", func(title, msg string) {
		p.appendLog("notify: " + title + " " + msg)
	})
	_ = vm.Set("settings", p.cfg.Settings)

	if _, err := vm.RunString(hostSetup); err != nil {
		return err
	}
	// 运行用户脚本,定义 onRequest/onResponse/onWebSocketMessage。
	if _, err := vm.RunString(p.cfg.Source); err != nil {
		return err
	}
	// 非严格模式编译,避免对桥接全局变量赋值时的严格模式限制。
	driver, err := goja.Compile(p.cfg.ID+"-driver", driverSrc, false)
	if err != nil {
		return err
	}
	p.vm = vm
	p.driver = driver
	return nil
}

func (p *Plugin) loop() {
	for {
		select {
		case <-p.quit:
			return
		case j := <-p.mailbox:
			j.reply <- p.run(j.phase, j.in)
		}
	}
}

// run 在 VM 中执行一次钩子(仅由 loop goroutine 调用)。
func (p *Plugin) run(phase string, in []byte) []byte {
	_ = p.vm.Set("__IN__", string(in))
	_ = p.vm.Set("__PHASE__", phase)

	timer := time.AfterFunc(p.timeout, func() { p.vm.Interrupt("timeout") })
	_, err := p.vm.RunProgram(p.driver)
	timer.Stop()
	p.vm.ClearInterrupt()
	if err != nil {
		p.appendLog("runtime error: " + err.Error())
		return nil
	}
	out := p.vm.Get("__OUT__")
	if out == nil {
		return nil
	}
	return []byte(out.String())
}

// dispatch 把一个调用投递到邮箱并等待结果(带保护)。
func (p *Plugin) dispatch(phase string, in []byte) []byte {
	reply := make(chan []byte, 1)
	select {
	case p.mailbox <- &job{phase: phase, in: in, reply: reply}:
	case <-time.After(p.timeout * 3):
		return nil // 插件繁忙,失败开放
	case <-p.quit:
		return nil
	}
	select {
	case out := <-reply:
		return out
	case <-time.After(p.timeout * 3):
		return nil
	}
}

// ---- pipeline.Hook 接口 ----

func (p *Plugin) Name() string  { return p.cfg.ID }
func (p *Plugin) Priority() int { return p.cfg.Priority }
func (p *Plugin) Enabled() bool { return p.enabled.Load() }

// SetEnabled 启用/禁用插件。
func (p *Plugin) SetEnabled(v bool) { p.enabled.Store(v) }

// Match 按白/黑名单判断是否作用于该 URL。
func (p *Plugin) Match(url string) bool {
	for _, b := range p.cfg.Blacklist {
		if matchPattern(b, url) {
			return false
		}
	}
	if len(p.cfg.Whitelist) == 0 {
		return true
	}
	for _, wcard := range p.cfg.Whitelist {
		if matchPattern(wcard, url) {
			return true
		}
	}
	return false
}

// OnRequest 执行请求钩子。
func (p *Plugin) OnRequest(ctx context.Context, f *flow.Flow) flow.Decision {
	in, _ := json.Marshal(requestToJS(f))
	out := p.dispatch("request", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	return applyHTTP(f, out, flow.PhaseRequest)
}

// OnResponse 执行响应钩子。
func (p *Plugin) OnResponse(ctx context.Context, f *flow.Flow) flow.Decision {
	in, _ := json.Marshal(requestToJS(f))
	out := p.dispatch("response", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	return applyHTTP(f, out, flow.PhaseResponse)
}

// OnWebSocketMessage 执行 WS 钩子。
func (p *Plugin) OnWebSocketMessage(ctx context.Context, m *flow.WSMessage) flow.Decision {
	in, _ := json.Marshal(jsFlow{
		Direction: m.Direction,
		Type:      m.Type,
		Data:      string(m.Data),
		URL:       m.URL,
	})
	out := p.dispatch("ws", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	var res jsOut
	if json.Unmarshal(out, &res) == nil {
		m.Data = []byte(res.Flow.Data)
	}
	return decisionFromJS(res.Decision, flow.PhaseRequest)
}

// OnStreamMessage 执行流消息钩子(SSE / gRPC / 分块)。插件可就地改写 flow.data。
func (p *Plugin) OnStreamMessage(ctx context.Context, m *flow.StreamMessage) flow.Decision {
	in, _ := json.Marshal(jsFlow{
		Direction: m.Direction,
		Kind:      m.Kind,
		EventType: m.EventType,
		Data:      string(m.Data),
		URL:       m.URL,
	})
	out := p.dispatch("stream", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	var res jsOut
	if json.Unmarshal(out, &res) == nil {
		m.Data = []byte(res.Flow.Data)
	}
	return decisionFromJS(res.Decision, flow.PhaseResponse)
}

// Logs 返回最近的插件日志。
func (p *Plugin) Logs() []string {
	p.logsMu.Lock()
	defer p.logsMu.Unlock()
	out := make([]string, len(p.logs))
	copy(out, p.logs)
	return out
}

// Close 停止插件 goroutine。
func (p *Plugin) Close() {
	p.once.Do(func() { close(p.quit) })
}

func (p *Plugin) appendLog(line string) {
	p.logsMu.Lock()
	defer p.logsMu.Unlock()
	p.logs = append(p.logs, line)
	if len(p.logs) > 200 {
		p.logs = p.logs[len(p.logs)-200:]
	}
}

// ---- 转换 ----

func requestToJS(f *flow.Flow) jsFlow {
	v := jsFlow{ID: f.ID}
	if f.Request != nil {
		v.Method = f.Request.Method
		v.URL = f.Request.URL
		v.Host = f.Request.Host
		v.Path = f.Request.Path
		v.Headers = flatten(f.Request.Header)
		v.Body = string(f.Request.Body)
	}
	if f.Response != nil {
		v.Response = &jsResponse{
			Status:     f.Response.Status,
			StatusText: f.Response.StatusText,
			Headers:    flatten(f.Response.Header),
			Body:       string(f.Response.Body),
		}
	}
	if p := f.Process(); p != nil {
		v.Process = &jsProcess{Name: p.Name, PID: p.PID, Path: p.Path}
	}
	return v
}

// applyHTTP 把 VM 返回的 flow 应用回 Go flow.Flow,并返回处置。
func applyHTTP(f *flow.Flow, out []byte, phase flow.Phase) flow.Decision {
	var res jsOut
	if err := json.Unmarshal(out, &res); err != nil {
		return flow.ContinueDecision()
	}
	jf := res.Flow
	if f.Request != nil {
		f.Request.Method = jf.Method
		f.Request.URL = jf.URL
		if jf.Host != "" {
			f.Request.Host = jf.Host
		}
		if jf.Path != "" {
			f.Request.Path = jf.Path
		}
		if jf.Headers != nil {
			f.Request.Header = unflatten(jf.Headers)
		}
		f.Request.Body = []byte(jf.Body)
	}
	if jf.Response != nil {
		f.Response = &flow.Response{
			Status:     jf.Response.Status,
			StatusText: jf.Response.StatusText,
			Header:     unflatten(jf.Response.Headers),
			Body:       []byte(jf.Response.Body),
		}
	}
	f.Modified = true
	return decisionFromJS(res.Decision, phase)
}

func decisionFromJS(d jsDecision, phase flow.Phase) flow.Decision {
	switch d.Kind {
	case "mock":
		return flow.MockDecision(d.Reason)
	case "abort":
		return flow.AbortDecision(d.Status, d.Reason)
	case "breakpoint":
		return flow.BreakpointDecision(phase, d.Reason)
	default:
		return flow.ContinueDecision()
	}
}

func flatten(h map[string][]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func unflatten(m map[string]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = []string{v}
	}
	return out
}

// matchPattern 支持 *、prefix*、*suffix、精确匹配。
func matchPattern(pattern, s string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") && len(pattern) > 1 {
		return strings.Contains(s, strings.Trim(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(s, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(s, strings.TrimPrefix(pattern, "*"))
	}
	return pattern == s
}
