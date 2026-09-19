// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"net/textproto"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// Emitter 把事件广播到上层（实现见 internal/core.EventBus 的适配），保持 pipeline 与 core 解耦。
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

// 断点解除方式，随 breakpoint_resolved 事件广播。
const (
	ResolutionResumed = "resumed"
	ResolutionAborted = "aborted"
	ResolutionExpired = "expired"
)

type resumeMsg struct {
	action ResumeAction
	edit   *BreakpointEdit
}

type paused struct {
	flow   *flow.Flow
	phase  flow.Phase
	resume chan resumeMsg
	// extend 传递续期请求；deadline 保存最新截止时刻。
	extend chan struct{}
	// deadline 是本次暂停自动失效的时刻，读写需持 BreakpointManager.pausedMu。
	deadline time.Time
	// seq 是命中序号，用于保持暂停列表顺序。
	seq uint64
}

// BreakpointFlow 是断点事件的载荷，flow 字段与断点运行期信息位于同一 JSON 层级。
type BreakpointFlow struct {
	*flow.Flow
	// PausedUntil 是本次暂停自动失效的时刻。
	PausedUntil time.Time `json:"pausedUntil,omitempty"`
	// Resolution 仅出现在 breakpoint_resolved 上,取值见 Resolution* 常量。
	Resolution string `json:"resolution,omitempty"`
	// RequestHeaders / ResponseHeaders 按线上顺序与大小写导出头部。
	RequestHeaders  [][2]string `json:"requestHeaders,omitempty"`
	ResponseHeaders [][2]string `json:"responseHeaders,omitempty"`
	// RequestHeadersB64 / ResponseHeadersB64 与对应头部下标对齐，为经 JSON 会损坏的非法
	// UTF-8 值保留原始字节；值全为合法 UTF-8 时省略。
	RequestHeadersB64  []string `json:"requestHeadersB64,omitempty"`
	ResponseHeadersB64 []string `json:"responseHeadersB64,omitempty"`
}

// newBreakpointFlow 按暂停中的 flow 做一份对外快照。
func newBreakpointFlow(f *flow.Flow, phase flow.Phase, deadline time.Time) *BreakpointFlow {
	snap := f.Clone()
	snap.PausedAt = phase
	out := &BreakpointFlow{Flow: snap, PausedUntil: deadline}
	if snap.Request != nil {
		out.RequestHeaders = withHostRow(flow.OrderedRequestHeaders(snap.Request), snap.Request.Host)
		out.RequestHeadersB64 = flow.HeaderPairValuesB64(out.RequestHeaders)
	}
	if snap.Response != nil {
		out.ResponseHeaders = flow.OrderedResponseHeaders(snap.Response)
		out.ResponseHeadersB64 = flow.HeaderPairValuesB64(out.ResponseHeaders)
	}
	return out
}

// withHostRow 确保导出的请求头包含 Host 行；h2 入站与合成 flow 从 Request.Host 补充该字段。
func withHostRow(pairs [][2]string, host string) [][2]string {
	if host == "" {
		return pairs
	}
	for _, kv := range pairs {
		if textproto.CanonicalMIMEHeaderKey(kv[0]) == "Host" {
			return pairs
		}
	}
	return append([][2]string{{"Host", host}}, pairs...)
}

// BreakRule 是一条 URL 匹配的断点规则:命中的 flow 在所选阶段暂停。
// URL 支持 * 通配(整串匹配);不含 * 时按子串包含匹配。
type BreakRule struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	OnRequest  bool   `json:"onRequest"`
	OnResponse bool   `json:"onResponse"`
	Enabled    bool   `json:"enabled"`

	// 通配模式的编译缓存，持有 BreakpointManager.mu 时读写。
	// reSrc 保存编译时模式串，用于检测 URL 更新后的缓存失效。
	reSrc string
	re    *regexp.Regexp
}

// BreakpointManager 管理被断点暂停、等待 UI 放行的 flow。
type BreakpointManager struct {
	// pausedMu 保护暂停队列；mu 保护全局开关与规则，二者独立。
	pausedMu sync.Mutex
	paused   map[string]*paused
	// pauseSeq 是命中序号，用于暂停列表的稳定排序。
	pauseSeq uint64

	// mu 护全局开关与规则(热路径 ShouldBreakFor 每请求每阶段取一次)。
	mu      sync.Mutex
	emit    Emitter
	timeout time.Duration
	maxOpen int

	// 全局断点开关(UI 可"断在请求/响应")。
	breakRequest  bool
	breakResponse bool

	// URL 匹配的断点规则(按创建先后有序)。
	rules []*BreakRule
	// ruleVer 每次规则变动自增,随快照一起交给 persist 用于丢弃过期的落盘。
	ruleVer uint64

	// persist 接收规则快照并负责落盘，由装配层注入，在 mu 外调用。
	persist func([]*BreakRule) error
	// persistMu 串行化规则快照写入；persistedVer 记录已成功持久化的最高版本。
	// flush 仅处理版本号高于 persistedVer 的快照，保持持久化版本单调递增。
	persistMu    sync.Mutex
	persistedVer uint64
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

// ShouldBreak 返回给定阶段的全局断点开关状态。
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

// SetPersist 注入规则落盘回调(装配时调用一次,不与 CRUD 并发)。
func (b *BreakpointManager) SetPersist(fn func([]*BreakRule) error) { b.persist = fn }

// RestoreRules 用持久化规则替换当前集合，不触发 persist。
func (b *BreakpointManager) RestoreRules(rules []*BreakRule) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rules = make([]*BreakRule, 0, len(rules))
	for _, r := range rules {
		if r == nil {
			continue
		}
		cp := *r
		// 快照仅复制持久化字段，正则缓存字段保持零值。
		b.rules = append(b.rules, &cp)
	}
}

// rulesSnapshotLocked 返回规则副本与版本号，调用方需持有 mu。
func (b *BreakpointManager) rulesSnapshotLocked() ([]*BreakRule, uint64) {
	b.ruleVer++
	out := make([]*BreakRule, 0, len(b.rules))
	for _, r := range b.rules {
		cp := *r
		// 快照持久化规则字段，正则缓存作为运行期状态重新构建。
		cp.reSrc, cp.re = "", nil
		out = append(out, &cp)
	}
	return out, b.ruleVer
}

// flush 将规则快照交给落盘回调，并按版本号保持持久化顺序。
func (b *BreakpointManager) flush(snapshot []*BreakRule, ver uint64) {
	if b.persist == nil {
		return
	}
	b.persistMu.Lock()
	defer b.persistMu.Unlock()
	if ver <= b.persistedVer {
		return
	}
	// 仅在写入成功后推进 persistedVer。
	if err := b.persist(snapshot); err == nil {
		b.persistedVer = ver
	}
}

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

// AddRuleWithEnabled 以指定启用状态新增 URL 断点规则，并在同一临界区完成初始化。
func (b *BreakpointManager) AddRuleWithEnabled(url string, onReq, onResp, enabled bool) *BreakRule {
	b.mu.Lock()
	r := &BreakRule{
		ID:         "bp-" + flow.NewID()[:8],
		URL:        url,
		OnRequest:  onReq,
		OnResponse: onResp,
		Enabled:    enabled,
	}
	b.rules = append(b.rules, r)
	cp := *r
	snapshot, ver := b.rulesSnapshotLocked()
	b.mu.Unlock()

	b.flush(snapshot, ver)
	return &cp
}

// UpdateRule 更新指定规则的字段(空 URL 表示不改);返回是否存在。
func (b *BreakpointManager) UpdateRule(id, url string, onReq, onResp, enabled bool) bool {
	_, ok := b.UpdateRuleFields(id, url, onReq, onResp, &enabled)
	return ok
}

// UpdateRuleFields 更新指定规则的字段并返回更新后的副本；enabled 为 nil 时保留现值。
// 读取与更新在同一临界区完成。
func (b *BreakpointManager) UpdateRuleFields(id, url string, onReq, onResp bool, enabled *bool) (*BreakRule, bool) {
	b.mu.Lock()
	var cp BreakRule
	var snapshot []*BreakRule
	var ver uint64
	found := false
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
			cp, found = *r, true
			snapshot, ver = b.rulesSnapshotLocked()
			break
		}
	}
	b.mu.Unlock()

	if !found {
		return nil, false
	}
	b.flush(snapshot, ver)
	return &cp, true
}

// ToggleRule 启用/禁用一条规则并返回更新后的副本。
func (b *BreakpointManager) ToggleRule(id string, enabled bool) (*BreakRule, bool) {
	b.mu.Lock()
	var cp BreakRule
	var snapshot []*BreakRule
	var ver uint64
	found := false
	for _, r := range b.rules {
		if r.ID == id {
			r.Enabled = enabled
			cp, found = *r, true
			snapshot, ver = b.rulesSnapshotLocked()
			break
		}
	}
	b.mu.Unlock()

	if !found {
		return nil, false
	}
	b.flush(snapshot, ver)
	return &cp, true
}

// DeleteRule 删除一条规则，返回是否存在。
func (b *BreakpointManager) DeleteRule(id string) bool {
	b.mu.Lock()
	var snapshot []*BreakRule
	var ver uint64
	found := false
	for i, r := range b.rules {
		if r.ID == id {
			b.rules = append(b.rules[:i], b.rules[i+1:]...)
			found = true
			snapshot, ver = b.rulesSnapshotLocked()
			break
		}
	}
	b.mu.Unlock()

	if !found {
		return false
	}
	b.flush(snapshot, ver)
	return true
}

// matchesLocked 判断 URL 是否命中本规则，调用方需持有 BreakpointManager.mu。
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

// Pause 暂停处理器 goroutine，将 flow 交给 UI 编辑，直到放行或超时。
func (b *BreakpointManager) Pause(f *flow.Flow, phase flow.Phase) (abort bool) {
	prevState := f.State
	p := &paused{
		flow:   f,
		phase:  phase,
		resume: make(chan resumeMsg, 1),
		extend: make(chan struct{}, 1),
	}

	timeout := b.Timeout()
	b.pausedMu.Lock()
	if len(b.paused) >= b.maxOpen {
		b.pausedMu.Unlock()
		return false // 超过上限,失败开放
	}
	b.pauseSeq++
	p.seq = b.pauseSeq
	p.deadline = time.Now().Add(timeout)
	deadline := p.deadline
	// f 进入 paused 后由 List() 克隆读取，写入发生在发布前或摘除后。
	f.State = flow.StatePausedAtBreakpoint
	b.paused[f.ID] = p
	b.pausedMu.Unlock()

	// defer 负责在 emit 异常时释放暂停条目；unpublish 可重复调用。
	resolution := ResolutionResumed
	defer func() {
		b.unpublish(f.ID)
		resolved := newBreakpointFlow(f, phase, time.Time{})
		resolved.Resolution = resolution
		b.emit(evtBreakpointResolved, resolved)
	}()

	// 发布快照，供桌面与 WebSocket 异步序列化。
	b.emit(evtBreakpointHit, newBreakpointFlow(f, phase, deadline))

	apply := func(msg resumeMsg) bool {
		if msg.action == ResumeAbort {
			resolution = ResolutionAborted
			return true
		}
		// 仅在编辑产生变化时设置 Modified。
		if msg.edit.apply(f, phase) {
			f.Modified = true
		}
		f.State = prevState
		return false
	}

	for {
		select {
		case msg := <-p.resume:
			b.unpublish(f.ID)
			return apply(msg)
		case <-p.extend:
			// 续期只是把截止时刻推后;重新读一次即可,旧的计时器随本轮 select 一起丢弃。
			deadline = b.deadlineOf(f.ID, deadline)
		case <-time.After(time.Until(deadline)):
			// 摘除与读取处置通道在同一把锁内完成，按投递顺序处理放行消息。
			if msg, delivered := b.finish(f.ID, p); delivered {
				return apply(msg)
			}
			// 超时按 Continue 处理，放行未编辑的 flow。
			resolution = ResolutionExpired
			f.State = prevState
			if f.Metadata == nil {
				f.Metadata = map[string]any{}
			}
			f.Metadata["breakpointTimedOut"] = true
			return false
		}
	}
}

// finish 摘除条目并在同一次加锁内排空处置通道,返回是否有已投递但尚未处理的处置。
func (b *BreakpointManager) finish(id string, p *paused) (resumeMsg, bool) {
	b.pausedMu.Lock()
	defer b.pausedMu.Unlock()
	delete(b.paused, id)
	select {
	case msg := <-p.resume:
		return msg, true
	default:
		return resumeMsg{}, false
	}
}

// deadlineOf 读取暂停条目的截止时刻；条目已摘除时返回 fallback。
func (b *BreakpointManager) deadlineOf(id string, fallback time.Time) time.Time {
	b.pausedMu.Lock()
	defer b.pausedMu.Unlock()
	if p, ok := b.paused[id]; ok {
		return p.deadline
	}
	return fallback
}

// unpublish 从暂停列表摘除 flow，可重复调用。
func (b *BreakpointManager) unpublish(id string) {
	b.pausedMu.Lock()
	delete(b.paused, id)
	b.pausedMu.Unlock()
}

// Resume 放行暂停的 flow；edit 为 nil 表示原样放行，编辑内容先校验。
func (b *BreakpointManager) Resume(id string, edit *BreakpointEdit) error {
	// 先将头部编辑解析为最终字节，再执行校验和投递，保证校验与应用使用同一份头部值。
	if err := b.resolveEditedHeaders(id, edit); err != nil {
		return err
	}
	if err := edit.Validate(); err != nil {
		return err
	}
	return b.deliver(id, resumeMsg{action: ResumeContinue, edit: edit})
}

// resolveEditedHeaders 从暂停 flow 读取请求、响应头部作为还原基准，
// 将 edit 中的头部字节旁路解析后写回有序头对。
func (b *BreakpointManager) resolveEditedHeaders(id string, edit *BreakpointEdit) error {
	if edit == nil || (edit.Request == nil && edit.Response == nil) {
		return nil
	}
	var reqBasis, resBasis [][2]string
	b.pausedMu.Lock()
	if p, ok := b.paused[id]; ok && p.flow != nil {
		if p.flow.Request != nil {
			reqBasis = withHostRow(flow.OrderedRequestHeaders(p.flow.Request), p.flow.Request.Host)
		}
		if p.flow.Response != nil {
			resBasis = flow.OrderedResponseHeaders(p.flow.Response)
		}
	}
	b.pausedMu.Unlock()
	return edit.resolveHeaders(reqBasis, resBasis)
}

// Abort 阻断一个暂停的 flow。
func (b *BreakpointManager) Abort(id string) error {
	return b.deliver(id, resumeMsg{action: ResumeAbort})
}

// ResumeAll 原样放行当前暂停的 flow，并返回成功投递数量。
func (b *BreakpointManager) ResumeAll() int {
	return b.deliverAll(resumeMsg{action: ResumeContinue})
}

// AbortAll 阻断当前所有暂停中的 flow,返回投递成功的条数。
func (b *BreakpointManager) AbortAll() int {
	return b.deliverAll(resumeMsg{action: ResumeAbort})
}

func (b *BreakpointManager) deliverAll(msg resumeMsg) int {
	// 投递与摘除使用同一把锁。
	b.pausedMu.Lock()
	defer b.pausedMu.Unlock()
	n := 0
	for _, p := range b.paused {
		select {
		case p.resume <- msg:
			n++
		default:
		}
	}
	return n
}

// Extend 将暂停 flow 的截止时刻延后一整个周期，并广播新的截止时刻。
func (b *BreakpointManager) Extend(id string) (time.Time, bool) {
	timeout := b.Timeout()
	b.pausedMu.Lock()
	p, ok := b.paused[id]
	if !ok {
		b.pausedMu.Unlock()
		return time.Time{}, false
	}
	p.deadline = time.Now().Add(timeout)
	deadline := p.deadline
	// 在锁内生成快照，保证暂停状态与截止时刻一致。
	snap := newBreakpointFlow(p.flow, p.phase, deadline)
	b.pausedMu.Unlock()

	select {
	case p.extend <- struct{}{}:
	default: // 已有一次未处理的续期在排队,新截止时刻已写进 deadline,不会丢
	}
	b.emit(evtBreakpointHit, snap)
	return deadline, true
}

// deliver 将处置投递给挂起的处理器 goroutine，使用容量为 1 的非阻塞通道。
func (b *BreakpointManager) deliver(id string, msg resumeMsg) error {
	// 查表与投递在同一把锁内完成，通道发送保持非阻塞。
	b.pausedMu.Lock()
	defer b.pausedMu.Unlock()
	p, ok := b.paused[id]
	if !ok {
		return ErrBreakpointNotFound
	}
	// 在投递前校验与暂停 flow 相关的编辑内容。
	if err := msg.edit.validateAgainst(p.flow); err != nil {
		return err
	}
	select {
	case p.resume <- msg:
		return nil
	default:
		return ErrBreakpointNotFound
	}
}

// List 返回按命中顺序排列的暂停 flow 快照。
func (b *BreakpointManager) List() []*BreakpointFlow {
	b.pausedMu.Lock()
	defer b.pausedMu.Unlock()
	type entry struct {
		seq  uint64
		item *BreakpointFlow
	}
	entries := make([]entry, 0, len(b.paused))
	for _, p := range b.paused {
		entries = append(entries, entry{seq: p.seq, item: newBreakpointFlow(p.flow, p.phase, p.deadline)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].seq < entries[j].seq })
	out := make([]*BreakpointFlow, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.item)
	}
	return out
}

// Timeout 返回单次暂停的自动放行周期。
func (b *BreakpointManager) Timeout() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.timeout
}
