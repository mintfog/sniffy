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
	Port        int    `json:"port"`
	Host        string `json:"host"`
	EnableHTTPS bool   `json:"enableHTTPS"`
	Recording   bool   `json:"recording"`
	// Extra 保存前端可能附带的其它字段,原样回存。
	Extra map[string]any `json:"-"`
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
	cs.save()
	return cs.cfg
}
