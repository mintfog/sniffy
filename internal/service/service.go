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
	stats     *statsCollector
	rules     *ruleStore
	cfg       *configStore
	cert      *certStore
	bus       *core.EventBus
	recording atomic.Bool
	startTime time.Time
}

// New 构造 Service。configDir 为持久化目录(rules.json / config.json);为空则仅内存。
func New(c ca.CA, bus *core.EventBus, configDir string) *Service {
	var rulesPath, configPath string
	if configDir != "" {
		rulesPath = filepath.Join(configDir, "rules.json")
		configPath = filepath.Join(configDir, "config.json")
	}
	svc := &Service{
		sessions:  newSessionStore(0),
		ws:        newWSStore(0),
		stats:     newStatsCollector(),
		rules:     newRuleStore(rulesPath),
		cfg:       newConfigStore(configPath, AppConfig{Port: 8080, Host: "0.0.0.0", Recording: true}),
		cert:      newCertStore(c),
		bus:       bus,
		startTime: time.Now(),
	}
	svc.recording.Store(svc.cfg.get().Recording)
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
func (s *Service) UpdateConfig(patch map[string]any) AppConfig {
	c := s.cfg.update(patch)
	if v, ok := patch["recording"].(bool); ok {
		s.recording.Store(v)
	}
	return c
}

// ---- 证书 ----

// CertificatePEM 返回根 CA 证书 PEM。
func (s *Service) CertificatePEM() []byte { return s.cert.ExportPEM() }

// ---- 状态 ----

// UptimeSeconds 返回运行时长(秒)。
func (s *Service) UptimeSeconds() int64 {
	return int64(time.Since(s.startTime).Seconds())
}
