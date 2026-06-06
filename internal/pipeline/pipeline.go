// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"context"
	"sort"
	"sync"

	"github.com/mintfog/sniffy/internal/flow"
)

// Logger 是 pipeline 需要的最小日志接口。
type Logger interface {
	Debug(msg string, args ...any)
	Error(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Error(string, ...any) {}

// Pipeline 持有所有插件并在 flow 上编排执行,真正应用 Decision。
type Pipeline struct {
	mu        sync.RWMutex
	reqHooks  []RequestHook
	respHooks []ResponseHook
	wsHooks   []WSHook
	connHooks []ConnHook

	bp     *BreakpointManager
	logger Logger
}

// New 创建管道。emit 用于断点/插件事件广播,可为 nil。
func New(emit Emitter, logger Logger) *Pipeline {
	if logger == nil {
		logger = nopLogger{}
	}
	return &Pipeline{
		bp:     NewBreakpointManager(emit),
		logger: logger,
	}
}

// Breakpoints 返回断点管理器。
func (p *Pipeline) Breakpoints() *BreakpointManager { return p.bp }

// Register 把一个插件登记进管道(按其实现的接口分类),并按优先级排序。
func (p *Pipeline) Register(h Hook) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if rh, ok := h.(RequestHook); ok {
		p.reqHooks = append(p.reqHooks, rh)
		sort.SliceStable(p.reqHooks, func(i, j int) bool { return p.reqHooks[i].Priority() < p.reqHooks[j].Priority() })
	}
	if rh, ok := h.(ResponseHook); ok {
		p.respHooks = append(p.respHooks, rh)
		sort.SliceStable(p.respHooks, func(i, j int) bool { return p.respHooks[i].Priority() < p.respHooks[j].Priority() })
	}
	if wh, ok := h.(WSHook); ok {
		p.wsHooks = append(p.wsHooks, wh)
		sort.SliceStable(p.wsHooks, func(i, j int) bool { return p.wsHooks[i].Priority() < p.wsHooks[j].Priority() })
	}
	if ch, ok := h.(ConnHook); ok {
		p.connHooks = append(p.connHooks, ch)
		sort.SliceStable(p.connHooks, func(i, j int) bool { return p.connHooks[i].Priority() < p.connHooks[j].Priority() })
	}
}

// Clear 清空所有已注册插件(热重载时使用)。
func (p *Pipeline) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqHooks = nil
	p.respHooks = nil
	p.wsHooks = nil
	p.connHooks = nil
}

func (p *Pipeline) snapshotReq() []RequestHook {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]RequestHook, len(p.reqHooks))
	copy(out, p.reqHooks)
	return out
}

func (p *Pipeline) snapshotResp() []ResponseHook {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]ResponseHook, len(p.respHooks))
	copy(out, p.respHooks)
	return out
}

func (p *Pipeline) snapshotWS() []WSHook {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]WSHook, len(p.wsHooks))
	copy(out, p.wsHooks)
	return out
}

// OnRequest 依次执行所有请求插件,合并处置;遇到 Abort/Mock 立即短路。
// 若处置为 Breakpoint,在此同步暂停(调用方即处理器 goroutine),放行后返回 Continue/Abort。
func (p *Pipeline) OnRequest(ctx context.Context, f *flow.Flow) flow.Decision {
	decision := flow.ContinueDecision()
	url := ""
	if f.Request != nil {
		url = f.Request.URL
	}
	for _, h := range p.snapshotReq() {
		if !h.Enabled() || !h.Match(url) {
			continue
		}
		d := p.safeReq(ctx, h, f)
		decision = flow.Merge(decision, d)
		if decision.Kind == flow.Abort || decision.Kind == flow.Mock {
			return decision
		}
	}
	// 全局"断在请求"开关。
	if decision.Kind != flow.Breakpoint && p.bp.ShouldBreak(flow.PhaseRequest) {
		decision = flow.BreakpointDecision(flow.PhaseRequest, "global breakpoint")
	}
	if decision.Kind == flow.Breakpoint {
		if p.bp.Pause(f, flow.PhaseRequest) {
			return flow.AbortDecision(0, "aborted at breakpoint")
		}
		return flow.ContinueDecision()
	}
	return decision
}

// OnResponse 同 OnRequest,作用于响应阶段(无 Mock 语义)。
func (p *Pipeline) OnResponse(ctx context.Context, f *flow.Flow) flow.Decision {
	decision := flow.ContinueDecision()
	url := ""
	if f.Request != nil {
		url = f.Request.URL
	}
	for _, h := range p.snapshotResp() {
		if !h.Enabled() || !h.Match(url) {
			continue
		}
		d := p.safeResp(ctx, h, f)
		decision = flow.Merge(decision, d)
		if decision.Kind == flow.Abort {
			return decision
		}
	}
	if decision.Kind != flow.Breakpoint && p.bp.ShouldBreak(flow.PhaseResponse) {
		decision = flow.BreakpointDecision(flow.PhaseResponse, "global breakpoint")
	}
	if decision.Kind == flow.Breakpoint {
		if p.bp.Pause(f, flow.PhaseResponse) {
			return flow.AbortDecision(0, "aborted at breakpoint")
		}
		return flow.ContinueDecision()
	}
	return decision
}

// OnWebSocketMessage 依次执行 WS 插件,允许就地修改 m.Data。
func (p *Pipeline) OnWebSocketMessage(ctx context.Context, m *flow.WSMessage) flow.Decision {
	decision := flow.ContinueDecision()
	for _, h := range p.snapshotWS() {
		if !h.Enabled() || !h.Match(m.URL) {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					p.logger.Error("ws 插件 %s panic: %v", h.Name(), r)
				}
			}()
			decision = flow.Merge(decision, h.OnWebSocketMessage(ctx, m))
		}()
		if decision.Kind == flow.Abort {
			return decision
		}
	}
	return decision
}

// safeReq / safeResp 包裹插件调用,recover panic,失败开放为 Continue。
func (p *Pipeline) safeReq(ctx context.Context, h RequestHook, f *flow.Flow) (d flow.Decision) {
	d = flow.ContinueDecision()
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("请求插件 %s panic: %v", h.Name(), r)
			d = flow.ContinueDecision()
		}
	}()
	return h.OnRequest(ctx, f)
}

func (p *Pipeline) safeResp(ctx context.Context, h ResponseHook, f *flow.Flow) (d flow.Decision) {
	d = flow.ContinueDecision()
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("响应插件 %s panic: %v", h.Name(), r)
			d = flow.ContinueDecision()
		}
	}()
	return h.OnResponse(ctx, f)
}
