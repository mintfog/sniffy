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

// 断点解除的方式,随 breakpoint_resolved 一起广播。超时是失败开放,不说清楚的话
// 用户看到的就是编辑器里的东西凭空消失、而请求已经发了出去。
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
	// extend 传递续期请求(缓冲 1 + 非阻塞投递,不阻塞调用方);新的截止时刻另经
	// deadline 传递,故重复投递被丢弃也不会丢掉续期。
	extend chan struct{}
	// deadline 是本次暂停自动失效的时刻,持 BreakpointManager.pausedMu 才可读写。
	deadline time.Time
	// seq 是命中的先后序号,给列表一个不随续期变动的稳定顺序。
	seq uint64
}

// BreakpointFlow 是断点对外的载荷形状:嵌入 flow 使它的全部字段与断点自身的
// 运行期信息在 JSON 里同层,消费者按取普通 flow 的路径就能读到它们。
type BreakpointFlow struct {
	*flow.Flow
	// PausedUntil 是本次暂停自动失效的时刻。到点是失败开放,UI 不给倒计时的话,
	// 用户会在编辑到一半时被静默放行。
	PausedUntil time.Time `json:"pausedUntil,omitempty"`
	// Resolution 仅出现在 breakpoint_resolved 上,取值见 Resolution* 常量。
	Resolution string `json:"resolution,omitempty"`
	// RequestHeaders / ResponseHeaders 是按线上顺序与大小写导出的头部。
	// Flow.Header 是 map:顺序与「Host 排在第几行」在它那里已经不存在了,而断点编辑器
	// 是所见即所发的界面 —— 它必须照线上的样子渲染,回传的也是同一份有序列表。
	RequestHeaders  [][2]string `json:"requestHeaders,omitempty"`
	ResponseHeaders [][2]string `json:"responseHeaders,omitempty"`
}

// newBreakpointFlow 按暂停中的 flow 做一份对外快照。
func newBreakpointFlow(f *flow.Flow, phase flow.Phase, deadline time.Time) *BreakpointFlow {
	snap := f.Clone()
	snap.PausedAt = phase
	out := &BreakpointFlow{Flow: snap, PausedUntil: deadline}
	if snap.Request != nil {
		out.RequestHeaders = withHostRow(flow.OrderedRequestHeaders(snap.Request), snap.Request.Host)
	}
	if snap.Response != nil {
		out.ResponseHeaders = flow.OrderedResponseHeaders(snap.Response)
	}
	return out
}

// withHostRow 保证导出的请求头里有 Host 行。h2 入站与合成的 flow 没有原始头序列,
// Host 只存在于 Request.Host,不补进来编辑器里就看不到、也就改不了它。
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

	// 通配模式的编译缓存,持有 BreakpointManager.mu 时才可读写。
	// reSrc 是编译时的模式串,URL 改动后与之不等,缓存自然失效。
	reSrc string
	re    *regexp.Regexp
}

// BreakpointManager 管理被断点暂停、等待 UI 放行的 flow。
type BreakpointManager struct {
	// pausedMu 只护暂停队列。与 mu 分开是必须的:List 要在锁内深拷贝全部暂停 flow
	// (含消息体),而 mu 是每条流量每个阶段都要抢的热路径锁 —— 合用一把的话,UI 每次
	// 刷新暂停列表都会让整个代理停顿一次。两块状态之间没有任何依赖。
	pausedMu sync.Mutex
	paused   map[string]*paused
	// pauseSeq 是命中的先后序号,给暂停列表一个稳定的顺序:
	// 按截止时刻排会在续期后把那一条甩到末尾。
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

	// persist 在规则集合变动后拿到一份快照去落盘,由装配层注入。
	// 必须在 mu 之外调用:ShouldBreakFor 每请求每阶段都要取这把锁,
	// 在锁内写盘会把磁盘延迟摊到每一条流量上。
	persist func([]*BreakRule) error
	// persistMu 串行化落盘,persistedVer 记住已经写下去的版本。
	// 只有 mu 保证不了顺序:两次 CRUD 各自在锁外写盘,先取到快照的那次可能后落地,
	// 磁盘上就停在旧版本 —— 用户新加的规则重启即丢,正是持久化要消除的那种观感。
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

// SetPersist 注入规则落盘回调(装配时调用一次,不与 CRUD 并发)。
func (b *BreakpointManager) SetPersist(fn func([]*BreakRule) error) { b.persist = fn }

// RestoreRules 用持久化的规则整体替换当前集合(启动时调用一次)。
// 不触发 persist —— 刚从盘上读回来的东西没必要再写一遍。
func (b *BreakpointManager) RestoreRules(rules []*BreakRule) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rules = make([]*BreakRule, 0, len(rules))
	for _, r := range rules {
		if r == nil {
			continue
		}
		cp := *r
		// 正则缓存不跟着复制:reSrc/re 是私有字段,入参来自装配层的转换,本就是零值。
		b.rules = append(b.rules, &cp)
	}
}

// rulesSnapshotLocked 取一份规则副本与它的版本号供落盘,调用方需持有 mu。
// 副本而非原指针:落盘发生在锁外,交出原指针会与热路径读写正则缓存撞车。
func (b *BreakpointManager) rulesSnapshotLocked() ([]*BreakRule, uint64) {
	b.ruleVer++
	out := make([]*BreakRule, 0, len(b.rules))
	for _, r := range b.rules {
		cp := *r
		// 正则缓存不跟着走:它是热路径上的私有状态,落盘与装配层都用不到。
		cp.reSrc, cp.re = "", nil
		out = append(out, &cp)
	}
	return out, b.ruleVer
}

// flush 把规则快照交给落盘回调。必须在 mu 之外调用;版本号回退时直接丢弃,
// 保证磁盘上最终停在最新的那一份。
func (b *BreakpointManager) flush(snapshot []*BreakRule, ver uint64) {
	if b.persist == nil {
		return
	}
	b.persistMu.Lock()
	defer b.persistMu.Unlock()
	if ver <= b.persistedVer {
		return
	}
	// 写成功才推进版本号:失败仍推进的话,排在后面、版本更旧但内容更全的那份快照
	// 会被当成过期直接丢掉,磁盘上停在更早的状态,两次改动一起消失。
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

// AddRuleWithEnabled 以指定启用状态新增 URL 断点规则。
// 创建与设置 Enabled 在同一次加锁内完成，避免禁用规则被热路径短暂观察为启用。
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

// UpdateRuleFields 更新指定规则的字段并返回更新后的副本。
// enabled 为 nil 时保留现值；读取与更新在同一次加锁内完成，避免覆盖并发的启停操作。
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
	// f 进入 paused 后即被 List() 读到(Clone),故对它的写入只能在发布之前或摘除之后。
	f.State = flow.StatePausedAtBreakpoint
	b.paused[f.ID] = p
	b.pausedMu.Unlock()

	// defer 须注册在 emit 之前:emit 由装配层注入,它 panic 时条目会永久占住 maxOpen 名额。
	// unpublish 幂等,正常路径已在 select 分支里摘过。
	resolution := ResolutionResumed
	defer func() {
		b.unpublish(f.ID)
		resolved := newBreakpointFlow(f, phase, time.Time{})
		resolved.Resolution = resolution
		b.emit(evtBreakpointResolved, resolved)
	}()

	// 发布快照而非活指针:消费者(桌面/WS)异步序列化,放行后处理器会就地改写 Request/Response。
	b.emit(evtBreakpointHit, newBreakpointFlow(f, phase, deadline))

	apply := func(msg resumeMsg) bool {
		if msg.action == ResumeAbort {
			resolution = ResolutionAborted
			return true
		}
		// 只有真的改动过才标 Modified:原样放行的 flow 不该在流量表里显示成被改过。
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
			// 摘除与"最后看一眼通道"必须在同一次加锁里:投递方在锁内发送,所以摘除之后
			// 再也不会有新消息进来,而此刻通道里若已经躺着一条处置,它就是先于超时到达的,
			// 必须认账。否则用户会看到"点了放行、返回成功",实际请求走的是超时原样放行。
			if msg, delivered := b.finish(f.ID, p); delivered {
				return apply(msg)
			}
			// 超时:失败开放,放行未编辑的 flow。
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

// deadlineOf 读取暂停条目的当前截止时刻;条目已被摘除时返回 fallback
// (放行与续期同时到达,下一轮 select 会立刻收到放行消息)。
func (b *BreakpointManager) deadlineOf(id string, fallback time.Time) time.Time {
	b.pausedMu.Lock()
	defer b.pausedMu.Unlock()
	if p, ok := b.paused[id]; ok {
		return p.deadline
	}
	return fallback
}

// unpublish 把 flow 从暂停列表摘除(幂等)。List() 在同一把锁下 Clone,故返回后本管理器
// 不会再读到该 flow,可就地改写;但同一指针仍被 sessionStore 无锁读,那是既有约束。
func (b *BreakpointManager) unpublish(id string) {
	b.pausedMu.Lock()
	delete(b.paused, id)
	b.pausedMu.Unlock()
}

// Resume 放行一个暂停的 flow,edit 为 nil 表示原样放行。
// 编辑内容不合法时返回校验错误且**不放行** —— flow 继续按在断点上,用户改回来还能重来。
func (b *BreakpointManager) Resume(id string, edit *BreakpointEdit) error {
	if err := edit.Validate(); err != nil {
		return err
	}
	return b.deliver(id, resumeMsg{action: ResumeContinue, edit: edit})
}

// Abort 阻断一个暂停的 flow。
func (b *BreakpointManager) Abort(id string) error {
	return b.deliver(id, resumeMsg{action: ResumeAbort})
}

// ResumeAll 原样放行当前所有暂停中的 flow,返回投递成功的条数。
// 全局断点一开,一个页面几十个并发请求会同时断住,逐条点放行不是可用的操作。
func (b *BreakpointManager) ResumeAll() int {
	return b.deliverAll(resumeMsg{action: ResumeContinue})
}

// AbortAll 阻断当前所有暂停中的 flow,返回投递成功的条数。
func (b *BreakpointManager) AbortAll() int {
	return b.deliverAll(resumeMsg{action: ResumeAbort})
}

func (b *BreakpointManager) deliverAll(msg resumeMsg) int {
	// 投递与摘除同锁(理由见 deliver):锁外投递会把一条已经超时放行的 flow 报成"已处置"。
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

// Extend 把一个暂停中 flow 的超时往后推一个完整周期,并广播新的截止时刻。
// 编辑一份大 body 可能超过一个周期,而超时是失败开放 —— 没有续期,等于把改到一半的
// 请求悄悄发出去。返回新的截止时刻;flow 已不在暂停中时返回 false。
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
	// 与 List 同理:在锁内取快照,避免与放行后的就地改写竞态。
	snap := newBreakpointFlow(p.flow, p.phase, deadline)
	b.pausedMu.Unlock()

	select {
	case p.extend <- struct{}{}:
	default: // 已有一次未处理的续期在排队,新截止时刻已写进 deadline,不会丢
	}
	b.emit(evtBreakpointHit, snap)
	return deadline, true
}

// deliver 把处置投给挂起的处理器 goroutine。通道容量为 1 且非阻塞投递:UI 连点两下
// 时第二次返回 ErrBreakpointNotFound,而不是把调用方挂住。
func (b *BreakpointManager) deliver(id string, msg resumeMsg) error {
	// 查表与投递必须在同一次加锁内:分成两步的话,处理器可能在解锁之后、投递之前
	// 因超时走掉,而调用方仍会收到"已处置",界面上显示成功、线上却发的是未编辑的原件。
	// 通道容量为 1 且非阻塞发送,持锁期间不会阻塞。
	b.pausedMu.Lock()
	defer b.pausedMu.Unlock()
	p, ok := b.paused[id]
	if !ok {
		return ErrBreakpointNotFound
	}
	// 与 flow 相关的校验只能在这里做(拿得到 p.flow),同样必须早于投递:
	// 不合法就让它继续按在断点上,用户改回来还能重来。
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

// List 返回当前所有暂停中的 flow 的快照(避免与放行后的就地改写竞态)。
// 按命中先后排序:map 的遍历顺序是随机的,不排序则每次拉取列表都会重排。
// 排序键用命中序号而不是截止时刻——续期会把截止时刻推到最大,那一条就会跳到末尾。
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
