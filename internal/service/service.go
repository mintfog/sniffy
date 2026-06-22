// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
)

// Service 是整个应用的唯一真相源。两种 transport(headless / 桌面)都只调用它。
type Service struct {
	sessions  *sessionStore
	ws        *wsStore
	stream    *streamStore
	stats     *statsCollector
	rules     *ruleStore
	cfg       *configStore
	cert      *certStore
	bus       *core.EventBus
	recording atomic.Bool
	startTime time.Time
	// applyUpstream 由装配层注入,把上游代理地址下发给引擎(空串=直连)。为 nil 时静默跳过。
	applyUpstream func(addr string) error
	// applySystemProxy 由桌面装配层注入,把系统代理指向监听端口(true)或释放(false)。
	// 仅桌面端注入;headless 与浏览器预览下为 nil,静默跳过。
	applySystemProxy func(enabled bool) error
}

// New 构造 Service。configDir 为持久化目录(rules.json / config.json);为空则仅内存。
func New(c ca.CA, bus *core.EventBus, configDir string) *Service {
	var rulesPath, configPath string
	if configDir != "" {
		rulesPath = filepath.Join(configDir, "rules.json")
		configPath = filepath.Join(configDir, configFileName)
	}
	cfgStore := newConfigStore(configPath, AppConfig{Port: 8080, Recording: true, SystemProxy: true, AutoProxy: true})
	cfg := cfgStore.get()
	svc := &Service{
		sessions:  newSessionStore(cfg.MaxFlows),
		ws:        newWSStore(0),
		stream:    newStreamStore(0),
		stats:     newStatsCollector(),
		rules:     newRuleStore(rulesPath),
		cfg:       cfgStore,
		cert:      newCertStore(c),
		bus:       bus,
		startTime: time.Now(),
	}
	svc.recording.Store(cfg.Recording)
	return svc
}

// Bus 返回事件总线。
func (s *Service) Bus() *core.EventBus { return s.bus }

func (s *Service) emit(t core.EventType, payload any) {
	if s.bus != nil {
		s.bus.Emit(t, payload)
	}
}

// ---- 录制 ----

// IsRecording 返回当前是否在录制(抓包写入存储)。
func (s *Service) IsRecording() bool { return s.recording.Load() }

// StartRecording 开始录制。
func (s *Service) StartRecording() { s.recording.Store(true) }

// StopRecording 停止录制。
func (s *Service) StopRecording() { s.recording.Store(false) }

// ---- Flow 记录(由内置 CaptureInterceptor 调用) ----

// RecordFlowStarted 在读到请求时记录一个 pending 会话并广播。
func (s *Service) RecordFlowStarted(f *flow.Flow) {
	if !s.recording.Load() {
		return
	}
	s.sessions.put(f)
	s.emit(core.EventFlowStarted, SessionDTO(f))
}

// RecordFlowCompleted 在拿到响应/完成时更新会话、累加统计并广播。
func (s *Service) RecordFlowCompleted(f *flow.Flow) {
	if _, ok := s.sessions.get(f.ID); !ok {
		// 未在录制开始时记录过则忽略(保持一致)。
		if !s.recording.Load() {
			return
		}
	}
	s.sessions.put(f)
	s.stats.record(f)
	if dto := ResponseDTO(f); dto != nil {
		s.emit(core.EventFlowCompleted, dto)
	}
	// 同时广播完整会话,供新前端按需更新。
	s.emit(core.EventFlowUpdated, SessionDTO(f))
}

// RecordFlowUpdated 在异步补充信息(如进程)后更新并广播。
func (s *Service) RecordFlowUpdated(f *flow.Flow) {
	s.sessions.put(f)
	s.emit(core.EventFlowUpdated, SessionDTO(f))
}

// ---- 会话查询 ----

// Sessions 返回最新优先的分页会话与总数。
func (s *Service) Sessions(page, pageSize int) ([]HTTPSessionDTO, int) {
	flows, total := s.sessions.list(page, pageSize)
	out := make([]HTTPSessionDTO, 0, len(flows))
	for _, f := range flows {
		out = append(out, SessionDTO(f))
	}
	return out, total
}

// Session 返回单个会话。
func (s *Service) Session(id string) (HTTPSessionDTO, bool) {
	f, ok := s.sessions.get(id)
	if !ok {
		return HTTPSessionDTO{}, false
	}
	return SessionDTO(f), true
}

// RawFlow 返回底层 Flow(供断点编辑等)。
func (s *Service) RawFlow(id string) (*flow.Flow, bool) {
	return s.sessions.get(id)
}

// DeleteSession 删除一个会话。
func (s *Service) DeleteSession(id string) { s.sessions.delete(id) }

// ClearSessions 清空所有会话。
func (s *Service) ClearSessions() { s.sessions.clear() }

// ---- WebSocket 会话 ----

// RecordWSSession 存储/更新一条 WebSocket 会话并广播。
func (s *Service) RecordWSSession(ws *flow.WSSession) {
	if !s.recording.Load() {
		return
	}
	s.ws.put(ws)
	s.emit(core.EventWSMessage, WSSessionDTO(ws))
}

// WSSessions 返回分页 WebSocket 会话。
func (s *Service) WSSessions(page, pageSize int) ([]WSSessionDTOType, int) {
	list, total := s.ws.list(page, pageSize)
	out := make([]WSSessionDTOType, 0, len(list))
	for _, ws := range list {
		out = append(out, WSSessionDTO(ws))
	}
	return out, total
}

// WSSession 返回单个 WebSocket 会话。
func (s *Service) WSSession(id string) (WSSessionDTOType, bool) {
	ws, ok := s.ws.get(id)
	if !ok {
		return WSSessionDTOType{}, false
	}
	return WSSessionDTO(ws), true
}

// ---- 流式会话(SSE / gRPC / 分块流) ----

// RecordStreamSession 存储/更新一条流会话并广播。
func (s *Service) RecordStreamSession(ss *flow.StreamSession) {
	if !s.recording.Load() {
		return
	}
	s.stream.put(ss)
	s.emit(core.EventStreamMessage, StreamSessionDTO(ss))
}

// StreamSessions 返回分页流会话。
func (s *Service) StreamSessions(page, pageSize int) ([]StreamSessionDTOType, int) {
	list, total := s.stream.list(page, pageSize)
	out := make([]StreamSessionDTOType, 0, len(list))
	for _, ss := range list {
		out = append(out, StreamSessionDTO(ss))
	}
	return out, total
}

// StreamSession 返回单个流会话。
func (s *Service) StreamSession(id string) (StreamSessionDTOType, bool) {
	ss, ok := s.stream.get(id)
	if !ok {
		return StreamSessionDTOType{}, false
	}
	return StreamSessionDTO(ss), true
}

// ---- 统计 ----

// Statistics 返回统计快照。
func (s *Service) Statistics() StatisticsDTO { return s.stats.snapshot() }

// ---- 规则 ----

func (s *Service) Rules() []*InterceptRule               { return s.rules.list() }
func (s *Service) Rule(id string) (*InterceptRule, bool) { return s.rules.get(id) }
func (s *Service) CreateRule(r *InterceptRule) *InterceptRule {
	return s.rules.create(r)
}
func (s *Service) UpdateRule(id string, r *InterceptRule) (*InterceptRule, bool) {
	return s.rules.update(id, r)
}
func (s *Service) ToggleRule(id string, enabled bool) (*InterceptRule, bool) {
	return s.rules.toggle(id, enabled)
}
func (s *Service) DeleteRule(id string)         { s.rules.delete(id) }
func (s *Service) RuleStats() InterceptStatsDTO { return s.rules.stats() }

// ---- 配置 ----

func (s *Service) Config() AppConfig { return s.cfg.get() }

// SetUpstreamApplier 注入「下发上游代理地址到引擎」的回调(装配层在 New 之后调用)。
func (s *Service) SetUpstreamApplier(fn func(addr string) error) { s.applyUpstream = fn }

// SetSystemProxyApplier 注入「把系统代理指向/释放监听端口」的回调(桌面装配层调用)。
func (s *Service) SetSystemProxyApplier(fn func(enabled bool) error) { s.applySystemProxy = fn }

// SetSystemProxyState 记录系统代理当前开关(不触发应用动作),供桌面层启动时对齐状态。
func (s *Service) SetSystemProxyState(on bool) { s.cfg.setSystemProxy(on) }

func (s *Service) UpdateConfig(patch map[string]any) AppConfig {
	prevSystemProxy := s.cfg.get().SystemProxy
	c := s.cfg.update(patch)
	if v, ok := patch["recording"].(bool); ok {
		s.recording.Store(v)
	}
	if v, ok := patch["maxFlows"].(float64); ok && int(v) > 0 {
		s.sessions.setCap(int(v))
	}
	// 上游代理开关/地址即时生效:以合并后的最终配置下发(幂等,地址未变时引擎内部不会动连接池)。
	if s.applyUpstream != nil {
		_ = s.applyUpstream(c.EffectiveUpstream())
	}
	// 前端每次推送都带 systemProxy,故以「值变化」为准,避免无关配置变更反复执行外部命令。
	if v, ok := patch["systemProxy"].(bool); ok && v != prevSystemProxy && s.applySystemProxy != nil {
		_ = s.applySystemProxy(v)
	}
	return c
}

// ---- 证书 ----

// CertificatePEM 返回根 CA 证书 PEM。
func (s *Service) CertificatePEM() []byte { return s.cert.ExportPEM() }

// IOSMobileconfig 返回内嵌根证书的 iOS 配置描述文件(.mobileconfig)。
func (s *Service) IOSMobileconfig() []byte { return s.cert.ExportMobileconfig() }

// SetCA 替换证书存储使用的 CA(用于重新生成 CA 后刷新导出)。
func (s *Service) SetCA(c ca.CA) { s.cert.setCA(c) }

// ---- 重发(外部产生的 flow,不受 recording 开关限制) ----

// ImportFlowStarted 广播一条进行中的外部 flow(如重发)并存入会话。
func (s *Service) ImportFlowStarted(f *flow.Flow) {
	s.sessions.put(f)
	s.emit(core.EventFlowStarted, SessionDTO(f))
}

// ImportFlowCompleted 更新并广播一条完成的外部 flow,累加统计。
func (s *Service) ImportFlowCompleted(f *flow.Flow) {
	s.sessions.put(f)
	s.stats.record(f)
	s.emit(core.EventFlowUpdated, SessionDTO(f))
}

// ---- 状态 ----

// UptimeSeconds 返回运行时长(秒)。
func (s *Service) UptimeSeconds() int64 {
	return int64(time.Since(s.startTime).Seconds())
}
