// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package js 用 goja 实现 JavaScript 插件层：请求、响应、WebSocket 与流消息钩子。
// 每个插件独占一个 Runtime 和邮箱 goroutine；Flow 通过 JSON 进入与离开 VM。
// 脚本抛错、超时或邮箱拥塞时按原值放行；回传值中形态非法的字段逐个丢弃，其余字段仍生效。
// 头部以首值扁平视图交互，回写时保留未修改字段的多值与顺序。
// 载荷在 VM 边界区分文本与 base64 两个通道，合法 UTF-8 走文本字段。
package js

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
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

// LogEntry 是一条结构化插件日志(供 UI 按级别过滤与展示)。
type LogEntry struct {
	Level string `json:"level"` // log|info|warn|error|debug|notify
	Msg   string `json:"msg"`
	Time  int64  `json:"time"` // Unix 毫秒
}

// Config 描述 JS 插件的元信息、脚本、持久化状态和日志回调。
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

	// StatePath 指定 store 落盘路径（<插件目录>/state.json）；省略时 store 驻留内存。
	StatePath string
	// InitialStore 是热重载时迁移到新实例的 store，优先于 StatePath。
	InitialStore map[string]any
	// OnLog 在每条插件日志产生时回调(管理器据此向 UI 实时推送)。可为 nil。
	OnLog func(LogEntry)
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

	logger Logger

	store       map[string]any
	storeMu     sync.Mutex
	storeDirty  atomic.Bool
	flusherDone chan struct{} // storeFlusher 退出信号(StatePath 非空时启用)
	tmpNonce    string        // 落盘临时文件名后缀,避免新旧实例并发写撞同一 .tmp

	logsMu sync.Mutex
	logs   []LogEntry

	// runMu 保护运行代次与 VM 中断状态；runGen 标识当前运行，超时回调仅作用于对应代次。
	runMu  sync.Mutex
	runGen uint64
}

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
	// store 初值:热重载迁移优先,其次磁盘,最后空。
	init := cfg.InitialStore
	if init == nil {
		init = loadStore(cfg.StatePath)
	}
	for k, v := range init {
		p.store[k] = v
	}
	if err := p.initVM(); err != nil {
		return nil, err
	}
	go p.loop()
	if cfg.StatePath != "" {
		p.tmpNonce = randHexBytes(4)
		p.flusherDone = make(chan struct{})
		go p.storeFlusher()
	}
	return p, nil
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
	return p.runHTTP("request", f, flow.PhaseRequest)
}

// OnResponse 执行响应钩子。
func (p *Plugin) OnResponse(ctx context.Context, f *flow.Flow) flow.Decision {
	return p.runHTTP("response", f, flow.PhaseResponse)
}

// runHTTP 执行请求/响应钩子的公共逻辑。
func (p *Plugin) runHTTP(phase string, f *flow.Flow, ph flow.Phase) flow.Decision {
	// 保存送入 VM 的视图，回程值以此作为字段改动比较基准。
	sent := requestToJS(f)
	in, _ := json.Marshal(&sent)
	out := p.dispatch(phase, in)
	if out == nil {
		return flow.ContinueDecision()
	}
	return applyHTTP(f, &sent, out, ph, p.appendLog)
}

// OnWebSocketMessage 执行 WS 钩子。
func (p *Plugin) OnWebSocketMessage(ctx context.Context, m *flow.WSMessage) flow.Decision {
	text, b64 := payloadToJS(m.Data)
	in, _ := json.Marshal(jsFlow{
		Direction: m.Direction,
		Type:      m.Type,
		Data:      text,
		DataB64:   b64,
		URL:       m.URL,
	})
	out := p.dispatch("ws", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	res, dropped, ok := parseOut(out, p.appendLog)
	if !ok {
		return flow.ContinueDecision()
	}
	p.applyMessagePayload(&m.Data, res.Flow, dropped)
	return decisionFromJS(res.Decision, flow.PhaseRequest)
}

// OnStreamMessage 执行流消息钩子(SSE / gRPC / 分块)。插件可就地改写 flow.data。
func (p *Plugin) OnStreamMessage(ctx context.Context, m *flow.StreamMessage) flow.Decision {
	text, b64 := payloadToJS(m.Data)
	in, _ := json.Marshal(jsFlow{
		Direction: m.Direction,
		Kind:      m.Kind,
		EventType: m.EventType,
		Data:      text,
		DataB64:   b64,
		URL:       m.URL,
	})
	out := p.dispatch("stream", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	res, dropped, ok := parseOut(out, p.appendLog)
	if !ok {
		return flow.ContinueDecision()
	}
	p.applyMessagePayload(&m.Data, res.Flow, dropped)
	return decisionFromJS(res.Decision, flow.PhaseResponse)
}

// applyMessagePayload 将 WS / 流钩子的载荷写回消息，仅在字节变化时更新原始切片。
func (p *Plugin) applyMessagePayload(data *[]byte, jf jsFlow, dropped dropSet) {
	if dropped.has("flow.data", "flow.dataB64") {
		return
	}
	b, ok := resolvePayload(jf.Data, jf.DataB64, "flow.data",
		"flow.dataB64 不是合法的标准 base64,消息载荷保持原值", p.appendLog)
	if !ok || bytes.Equal(b, *data) {
		return
	}
	*data = b
}

// Logs 返回最近的插件日志(结构化,供 UI 按级别过滤)。
func (p *Plugin) Logs() []LogEntry {
	p.logsMu.Lock()
	defer p.logsMu.Unlock()
	out := make([]LogEntry, len(p.logs))
	copy(out, p.logs)
	return out
}

// ClearLogs 清空插件日志环形缓冲。
func (p *Plugin) ClearLogs() {
	p.logsMu.Lock()
	p.logs = nil
	p.logsMu.Unlock()
}

// Snapshot 返回 store 的深拷贝（store 内容均为 JSON 形态），供热重载迁移到新实例。
// 迁移结果记录被跳过的不可序列化路径。
func (p *Plugin) Snapshot() map[string]any {
	p.storeMu.Lock()
	out, dropped := deepCopyJSON(p.store)
	p.storeMu.Unlock()
	if len(dropped) > 0 {
		p.appendLog("error", "store 快照跳过了不可序列化的键: "+strings.Join(dropped, ", "))
	}
	return out
}

// Close 停止插件 goroutine，等待 store 刷写 goroutine 退出后完成最终落盘。
func (p *Plugin) Close() {
	p.once.Do(func() {
		close(p.quit)
		if p.flusherDone != nil {
			<-p.flusherDone
		}
		p.flushStore()
	})
}

func (p *Plugin) appendLog(level, msg string) {
	e := LogEntry{Level: level, Msg: msg, Time: time.Now().UnixMilli()}
	p.logsMu.Lock()
	p.logs = append(p.logs, e)
	if len(p.logs) > 200 {
		p.logs = p.logs[len(p.logs)-200:]
	}
	p.logsMu.Unlock()
	if p.cfg.OnLog != nil {
		p.cfg.OnLog(e)
	}
}

// ---- store 持久化 ----

func (p *Plugin) storeFlusher() {
	defer close(p.flusherDone)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.quit:
			return
		case <-t.C:
			if p.storeDirty.Swap(false) {
				p.flushStore()
			}
		}
	}
}

func (p *Plugin) flushStore() {
	if p.cfg.StatePath == "" {
		return
	}
	p.storeMu.Lock()
	data, err := json.Marshal(p.store)
	var dropped []string
	if err != nil {
		// 按键递归复制可序列化数据，并更新 store。
		var clean map[string]any
		clean, dropped = deepCopyJSONPerKey(p.store)
		if data, err = json.Marshal(clean); err == nil {
			p.store = clean
		}
	}
	p.storeMu.Unlock()
	if len(dropped) > 0 {
		p.appendLog("error", "store 落盘跳过了不可序列化的键: "+strings.Join(dropped, ", "))
	}
	if err != nil {
		return
	}
	// 每个实例使用独立临时文件名，最终通过 rename 原子替换目标文件。
	tmp := p.cfg.StatePath + ".tmp." + p.tmpNonce
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, p.cfg.StatePath)
	}
}

// randHexBytes 返回 n 字节随机数据的 hex 串。
func randHexBytes(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loadStore(path string) map[string]any {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

// deepCopyJSON 通过 JSON 深拷贝 map，并返回跳过的键路径。
func deepCopyJSON(m map[string]any) (map[string]any, []string) {
	if len(m) == 0 {
		return map[string]any{}, nil
	}
	if b, err := json.Marshal(m); err == nil {
		var out map[string]any
		if json.Unmarshal(b, &out) == nil {
			return out, nil
		}
	}
	return deepCopyJSONPerKey(m)
}

// deepCopyJSONPerKey 逐键递归拷贝 map，并记录不可序列化的路径。
func deepCopyJSONPerKey(m map[string]any) (map[string]any, []string) {
	var dropped []string
	out := make(map[string]any, len(m))
	seen := make(map[uintptr]bool)
	for k, v := range m {
		if cv, ok := copyValueJSON(v, k, seen, 0, &dropped); ok {
			out[k] = cv
		}
	}
	return out, dropped
}

// copyValueJSON 递归拷贝值；seen 记录当前路径上的 map/slice，循环引用按路径记录并跳过。
func copyValueJSON(v any, path string, seen map[uintptr]bool, depth int, dropped *[]string) (any, bool) {
	if b, err := json.Marshal(v); err == nil {
		var out any
		if json.Unmarshal(b, &out) == nil {
			return out, true
		}
	}
	if depth < 64 {
		switch t := v.(type) {
		case map[string]any:
			ptr := reflect.ValueOf(t).Pointer()
			if seen[ptr] {
				break
			}
			seen[ptr] = true
			out := make(map[string]any, len(t))
			for k, sv := range t {
				if cv, ok := copyValueJSON(sv, path+"."+k, seen, depth+1, dropped); ok {
					out[k] = cv
				}
			}
			delete(seen, ptr)
			return out, true
		case []any:
			ptr := reflect.ValueOf(t).Pointer()
			if seen[ptr] {
				break
			}
			seen[ptr] = true
			out := make([]any, 0, len(t))
			for i, sv := range t {
				if cv, ok := copyValueJSON(sv, path+"["+strconv.Itoa(i)+"]", seen, depth+1, dropped); ok {
					out = append(out, cv)
				}
			}
			delete(seen, ptr)
			return out, true
		}
	}
	*dropped = append(*dropped, path)
	return nil, false
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
