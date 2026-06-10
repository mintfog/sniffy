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
		if wildcardMatch(r.URL, url) {
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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ruleSeq++
	r := &BreakRule{
		ID:         "bp-" + flow.NewID()[:8],
		URL:        url,
		OnRequest:  onReq,
		OnResponse: onResp,
		Enabled:    true,
	}
	b.rules = append(b.rules, r)
	cp := *r
	return &cp
}

// UpdateRule 更新指定规则的字段(空 URL 表示不改);返回是否存在。
func (b *BreakpointManager) UpdateRule(id, url string, onReq, onResp, enabled bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.rules {
		if r.ID == id {
			if url != "" {
				r.URL = url
			}
			r.OnRequest = onReq
			r.OnResponse = onResp
			r.Enabled = enabled
			return true
		}
	}
	return false
}

// ToggleRule 启用/禁用一条规则;返回是否存在。
func (b *BreakpointManager) ToggleRule(id string, enabled bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.rules {
		if r.ID == id {
			r.Enabled = enabled
			return true
		}
	}
	return false
}

// DeleteRule 删除一条规则。
func (b *BreakpointManager) DeleteRule(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, r := range b.rules {
		if r.ID == id {
			b.rules = append(b.rules[:i], b.rules[i+1:]...)
			return
		}
	}
}

// wildcardMatch 用 * 通配(整串匹配)判断 url 是否匹配 pattern;
// pattern 不含 * 时退化为子串包含匹配,空 pattern 不匹配。
func wildcardMatch(pattern, url string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return strings.Contains(url, pattern)
	}
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
		return false
	}
	return re.MatchString(url)
}

// Pause 暂停当前 goroutine(处理器),把 flow 交给 UI 手动编辑,直到放行或超时。
// 返回是否应阻断该 flow。它会就地把 UI 编辑后的内容合并回 f。
func (b *BreakpointManager) Pause(f *flow.Flow, phase flow.Phase) (abort bool) {
	b.mu.Lock()
	if len(b.paused) >= b.maxOpen {
		b.mu.Unlock()
		return false // 超过上限,失败开放
	}
	p := &paused{flow: f, phase: phase, resume: make(chan resumeMsg, 1)}
	b.paused[f.ID] = p
	b.mu.Unlock()

	prevState := f.State
	f.State = flow.StatePausedAtBreakpoint
	// 发布快照而非活指针:消费者(桌面/WS)异步序列化,放行后处理器会就地替换 Request/Response。
	b.emit(evtBreakpointHit, f.Clone())

	defer func() {
		b.mu.Lock()
		delete(b.paused, f.ID)
		b.mu.Unlock()
		b.emit(evtBreakpointResolved, f.Clone())
	}()

	select {
	case msg := <-p.resume:
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
		// 超时:失败开放,放行未编辑的 flow。
		f.State = prevState
		if f.Metadata == nil {
			f.Metadata = map[string]any{}
		}
		f.Metadata["breakpointTimedOut"] = true
		return false
	}
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
		out = append(out, p.flow.Clone())
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
