// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// Emitter 把事件广播到上层(实现见 internal/core.EventBus 的适配)。
// 用函数类型避免 pipeline 反向依赖 core(防止 import 环)。
type Emitter func(eventType string, payload any)

// 断点相关事件类型(与 core.EventType 字符串一致)。
const (
	evtBreakpointHit      = "breakpoint_hit"
	evtBreakpointResolved = "breakpoint_resolved"
)

// ResumeAction 是 UI 对一个暂停 flow 的处置。
type ResumeAction string

const (
	ResumeContinue ResumeAction = "continue" // 用(可能编辑过的)flow 继续
	ResumeAbort    ResumeAction = "abort"    // 阻断
)

type resumeMsg struct {
	action ResumeAction
	edited *flow.Flow
}

type paused struct {
	flow   *flow.Flow
	phase  flow.Phase
	resume chan resumeMsg
}

// BreakRule 是一条 URL 匹配的断点规则:命中的 flow 在所选阶段暂停。
// URL 支持 * 通配(整串匹配);不含 * 时按子串包含匹配。
type BreakRule struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	OnRequest  bool   `json:"onRequest"`
	OnResponse bool   `json:"onResponse"`
	Enabled    bool   `json:"enabled"`

	// 通配模式的编译缓存,持有 BreakpointManager.mu 时才可读写。
	// reSrc 是编译时的模式串,URL 改动后与之不等,缓存自然失效。
	reSrc string
	re    *regexp.Regexp
}

// BreakpointManager 管理被断点暂停、等待 UI 放行的 flow。
type BreakpointManager struct {
	mu      sync.Mutex
	paused  map[string]*paused
	emit    Emitter
	timeout time.Duration
	maxOpen int

	// 全局断点开关(UI 可"断在请求/响应")。
	breakRequest  bool
	breakResponse bool

	// URL 匹配的断点规则(按 ID 有序)。
	rules   []*BreakRule
	ruleSeq int
}

// NewBreakpointManager 创建断点管理器。
func NewBreakpointManager(emit Emitter) *BreakpointManager {
	if emit == nil {
		emit = func(string, any) {}
	}
	return &BreakpointManager{
		paused:  make(map[string]*paused),
		emit:    emit,
		timeout: 5 * time.Minute,
		maxOpen: 100,
	}
}

// SetGlobalBreak 设置全局"断在请求/响应"开关。
func (b *BreakpointManager) SetGlobalBreak(onRequest, onResponse bool) {
	b.mu.Lock()
	b.breakRequest = onRequest
	b.breakResponse = onResponse
	b.mu.Unlock()
}

// GlobalBreak 返回当前全局断点开关。
func (b *BreakpointManager) GlobalBreak() (onRequest, onResponse bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.breakRequest, b.breakResponse
}

// ShouldBreak 返回给定阶段是否应触发全局断点(不考虑 URL 规则)。
func (b *BreakpointManager) ShouldBreak(phase flow.Phase) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.globalForLocked(phase)
}

// ShouldBreakFor 返回给定 URL/阶段是否应触发断点:全局开关命中,
// 或任一启用的 URL 规则匹配该 URL 且覆盖该阶段。
func (b *BreakpointManager) ShouldBreakFor(url string, phase flow.Phase) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.globalForLocked(phase) {
		return true
	}
	for _, r := range b.rules {
		if !r.Enabled {
			continue
		}
		if phase == flow.PhaseRequest && !r.OnRequest {
			continue
		}
		if phase == flow.PhaseResponse && !r.OnResponse {
			continue
		}
		if r.matchesLocked(url) {
			return true
		}
	}
	return false
}

func (b *BreakpointManager) globalForLocked(phase flow.Phase) bool {
	switch phase {
	case flow.PhaseRequest:
		return b.breakRequest
	case flow.PhaseResponse:
		return b.breakResponse
	}
	return false
}

// ---- URL 断点规则 CRUD ----

// ListRules 返回当前所有 URL 断点规则的副本。
func (b *BreakpointManager) ListRules() []*BreakRule {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*BreakRule, 0, len(b.rules))
	for _, r := range b.rules {
		cp := *r
		out = append(out, &cp)
	}
	return out
}

// AddRule 新增一条 URL 断点规则并返回它(含生成的 ID)。
func (b *BreakpointManager) AddRule(url string, onReq, onResp bool) *BreakRule {
	return b.AddRuleWithEnabled(url, onReq, onResp, true)
}

// AddRuleWithEnabled 以指定启用状态新增 URL 断点规则。
// 创建与设置 Enabled 在同一次加锁内完成，避免禁用规则被热路径短暂观察为启用。
func (b *BreakpointManager) AddRuleWithEnabled(url string, onReq, onResp, enabled bool) *BreakRule {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ruleSeq++
	r := &BreakRule{
		ID:         "bp-" + flow.NewID()[:8],
		URL:        url,
		OnRequest:  onReq,
		OnResponse: onResp,
		Enabled:    enabled,
	}
	b.rules = append(b.rules, r)
	cp := *r
	return &cp
}

// UpdateRule 更新指定规则的字段(空 URL 表示不改);返回是否存在。
func (b *BreakpointManager) UpdateRule(id, url string, onReq, onResp, enabled bool) bool {
	_, ok := b.UpdateRuleFields(id, url, onReq, onResp, &enabled)
	return ok
}

// UpdateRuleFields 更新指定规则的字段并返回更新后的副本。
// enabled 为 nil 时保留现值；读取与更新在同一次加锁内完成，避免覆盖并发的启停操作。
func (b *BreakpointManager) UpdateRuleFields(id, url string, onReq, onResp bool, enabled *bool) (*BreakRule, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.rules {
		if r.ID == id {
			if url != "" {
				r.URL = url
			}
			r.OnRequest = onReq
			r.OnResponse = onResp
			if enabled != nil {
				r.Enabled = *enabled
			}
			cp := *r
			return &cp, true
		}
	}
	return nil, false
}

// ToggleRule 启用/禁用一条规则并返回更新后的副本。
func (b *BreakpointManager) ToggleRule(id string, enabled bool) (*BreakRule, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.rules {
		if r.ID == id {
			r.Enabled = enabled
			cp := *r
			return &cp, true
		}
	}
	return nil, false
}

// DeleteRule 删除一条规则，返回是否存在。
func (b *BreakpointManager) DeleteRule(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, r := range b.rules {
		if r.ID == id {
			b.rules = append(b.rules[:i], b.rules[i+1:]...)
			return true
		}
	}
	return false
}

// matchesLocked 判断 url 是否命中本规则(语义见 BreakRule),调用方需持有 BreakpointManager.mu。
// 编译结果必须缓存:ShouldBreakFor 每请求每阶段遍历全部规则,现场编译会慢一个数量级。
func (r *BreakRule) matchesLocked(url string) bool {
	pattern := strings.TrimSpace(r.URL)
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return strings.Contains(url, pattern)
	}
	if r.reSrc != pattern {
		r.re = compileWildcard(pattern)
		r.reSrc = pattern
	}
	return r.re != nil && r.re.MatchString(url)
}

// compileWildcard 把含 * 的模式编译成整串匹配的正则。字面量经 QuoteMeta 转义,
// 故 . ? + ( ) 等按字面量处理。
func compileWildcard(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i, lit := range strings.Split(pattern, "*") {
		if i > 0 {
			b.WriteString(".*")
		}
		b.WriteString(regexp.QuoteMeta(lit))
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

// Pause 暂停当前 goroutine(处理器),把 flow 交给 UI 手动编辑,直到放行或超时。
// 返回是否应阻断该 flow。它会就地把 UI 编辑后的内容合并回 f。
func (b *BreakpointManager) Pause(f *flow.Flow, phase flow.Phase) (abort bool) {
	prevState := f.State
	p := &paused{flow: f, phase: phase, resume: make(chan resumeMsg, 1)}

	b.mu.Lock()
	if len(b.paused) >= b.maxOpen {
		b.mu.Unlock()
		return false // 超过上限,失败开放
	}
	// f 进入 paused 后即被 List() 读到(Clone),故对它的写入只能在发布之前或摘除之后。
	f.State = flow.StatePausedAtBreakpoint
	b.paused[f.ID] = p
	b.mu.Unlock()

	// defer 须注册在 emit 之前:emit 由装配层注入,它 panic 时条目会永久占住 maxOpen 名额。
	// unpublish 幂等,正常路径已在 select 分支里摘过。
	defer func() {
		b.unpublish(f.ID)
		resolved := f.Clone()
		resolved.PausedAt = phase
		b.emit(evtBreakpointResolved, resolved)
	}()

	// 发布快照而非活指针:消费者(桌面/WS)异步序列化,放行后处理器会就地替换 Request/Response。
	hit := f.Clone()
	hit.PausedAt = phase
	b.emit(evtBreakpointHit, hit)

	select {
	case msg := <-p.resume:
		b.unpublish(f.ID)
		if msg.action == ResumeAbort {
			return true
		}
		if msg.edited != nil {
			mergeFlow(f, msg.edited)
			f.Modified = true
		}
		f.State = prevState
		return false
	case <-time.After(b.timeout):
		b.unpublish(f.ID)
		// 超时:失败开放,放行未编辑的 flow。
		f.State = prevState
		if f.Metadata == nil {
			f.Metadata = map[string]any{}
		}
		f.Metadata["breakpointTimedOut"] = true
		return false
	}
}

// unpublish 把 flow 从暂停列表摘除(幂等)。List() 在同一把锁下 Clone,故返回后本管理器
// 不会再读到该 flow,可就地改写;但同一指针仍被 sessionStore 无锁读,那是既有约束。
func (b *BreakpointManager) unpublish(id string) {
	b.mu.Lock()
	delete(b.paused, id)
	b.mu.Unlock()
}

// Resume 放行一个暂停的 flow(可携带 UI 编辑后的内容)。
func (b *BreakpointManager) Resume(id string, edited *flow.Flow) bool {
	return b.deliver(id, resumeMsg{action: ResumeContinue, edited: edited})
}

// Abort 阻断一个暂停的 flow。
func (b *BreakpointManager) Abort(id string) bool {
	return b.deliver(id, resumeMsg{action: ResumeAbort})
}

func (b *BreakpointManager) deliver(id string, msg resumeMsg) bool {
	b.mu.Lock()
	p, ok := b.paused[id]
	b.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case p.resume <- msg:
		return true
	default:
		return false
	}
}

// List 返回当前所有暂停中的 flow 的快照(避免与放行后的就地改写竞态)。
func (b *BreakpointManager) List() []*flow.Flow {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*flow.Flow, 0, len(b.paused))
	for _, p := range b.paused {
		item := p.flow.Clone()
		item.PausedAt = p.phase
		out = append(out, item)
	}
	return out
}

// mergeFlow 把 UI 编辑后的 src 合并进 dst(只覆盖请求/响应内容)。
func mergeFlow(dst, src *flow.Flow) {
	if src.Request != nil {
		dst.Request = src.Request
	}
	if src.Response != nil {
		dst.Response = src.Response
	}
}
