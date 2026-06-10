// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"os"
	"sync"
)

// AppConfig 对应前端 SniffyConfig 的核心字段(可持久化)。
type AppConfig struct {
	Port         int    `json:"port"`
	Host         string `json:"host"`
	EnableHTTPS  bool   `json:"enableHTTPS"`
	Recording    bool   `json:"recording"`
	MaxFlows     int    `json:"maxFlows,omitempty"` // 会话存储容量上限;0 取默认值
	Upstream     bool   `json:"upstream"`           // 是否启用上游(二级)代理
	UpstreamAddr string `json:"upstreamAddr"`       // 上游代理地址,如 http://host:port
	// Extra 保存前端可能附带的其它字段,原样回存。
	Extra map[string]any `json:"-"`
}

// EffectiveUpstream 返回实际生效的上游代理地址:开关关闭时为空(直连)。
func (c AppConfig) EffectiveUpstream() string {
	if !c.Upstream {
		return ""
	}
	return c.UpstreamAddr
}

type configStore struct {
	mu   sync.RWMutex
	cfg  AppConfig
	path string
}

func newConfigStore(path string, defaults AppConfig) *configStore {
	cs := &configStore{cfg: defaults, path: path}
	cs.load()
	return cs
}

func (cs *configStore) load() {
	if cs.path == "" {
		return
	}
	data, err := os.ReadFile(cs.path)
	if err != nil {
		return
	}
	var c AppConfig
	if json.Unmarshal(data, &c) == nil {
		cs.cfg = c
	}
}

func (cs *configStore) save() {
	if cs.path == "" {
		return
	}
	if data, err := json.MarshalIndent(cs.cfg, "", "  "); err == nil {
		_ = os.WriteFile(cs.path, data, 0o644)
	}
}

func (cs *configStore) get() AppConfig {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.cfg
}

// update 合并部分字段并持久化。
func (cs *configStore) update(patch map[string]any) AppConfig {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if v, ok := patch["port"].(float64); ok {
		cs.cfg.Port = int(v)
	}
	if v, ok := patch["host"].(string); ok {
		cs.cfg.Host = v
	}
	if v, ok := patch["enableHTTPS"].(bool); ok {
		cs.cfg.EnableHTTPS = v
	}
	if v, ok := patch["recording"].(bool); ok {
		cs.cfg.Recording = v
	}
	if v, ok := patch["maxFlows"].(float64); ok && int(v) > 0 {
		cs.cfg.MaxFlows = int(v)
	}
	if v, ok := patch["upstream"].(bool); ok {
		cs.cfg.Upstream = v
	}
	if v, ok := patch["upstreamAddr"].(string); ok {
		cs.cfg.UpstreamAddr = v
	}
	cs.save()
	return cs.cfg
}
