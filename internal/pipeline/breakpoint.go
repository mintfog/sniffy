// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
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

// ShouldBreak 返回给定阶段是否应触发全局断点。
func (b *BreakpointManager) ShouldBreak(phase flow.Phase) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch phase {
	case flow.PhaseRequest:
		return b.breakRequest
	case flow.PhaseResponse:
		return b.breakResponse
	}
	return false
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
	b.emit(evtBreakpointHit, f)

	defer func() {
		b.mu.Lock()
		delete(b.paused, f.ID)
		b.mu.Unlock()
		b.emit(evtBreakpointResolved, f)
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

// List 返回当前所有暂停中的 flow。
func (b *BreakpointManager) List() []*flow.Flow {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*flow.Flow, 0, len(b.paused))
	for _, p := range b.paused {
		out = append(out, p.flow)
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
