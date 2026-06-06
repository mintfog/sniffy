// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/plugin/js"
)

// Logger 是插件管理器需要的最小日志接口。
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
}

// loaded 是一个已加载的插件实例及其元信息。
type loaded struct {
	manifest Manifest
	dir      string
	plugin   *js.Plugin
}

// Manager 负责发现、加载 JS 插件并注册进管道,实现 api.PluginProvider。
type Manager struct {
	pipe   *pipeline.Pipeline
	dir    string
	logger Logger

	mu      sync.RWMutex
	plugins map[string]*loaded
}

// NewManager 创建插件管理器。dir 为用户插件根目录。
func NewManager(pipe *pipeline.Pipeline, dir string, logger Logger) *Manager {
	return &Manager{
		pipe:    pipe,
		dir:     dir,
		logger:  logger,
		plugins: make(map[string]*loaded),
	}
}

// LoadAll 扫描插件目录,加载全部插件并重建管道。
func (m *Manager) LoadAll() error {
	if m.dir == "" {
		return nil
	}
	m.seedExamples()

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}

	m.mu.Lock()
	// 关闭旧实例。
	for _, l := range m.plugins {
		l.plugin.Close()
	}
	m.plugins = make(map[string]*loaded)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.dir, e.Name())
		if l, err := m.loadOne(dir); err != nil {
			m.logf("error", "加载插件失败 %s: %v", e.Name(), err)
		} else {
			m.plugins[l.manifest.ID] = l
		}
	}
	m.mu.Unlock()

	m.rebuildPipeline()
	return nil
}

func (m *Manager) loadOne(dir string) (*loaded, error) {
	man, err := loadManifest(dir)
	if err != nil {
		return nil, err
	}
	source, err := os.ReadFile(filepath.Join(dir, man.Entry))
	if err != nil {
		return nil, err
	}
	p, err := js.NewPlugin(js.Config{
		ID:        man.ID,
		Name:      man.Name,
		Priority:  man.Priority,
		Enabled:   man.Enabled,
		Whitelist: man.Whitelist,
		Blacklist: man.Blacklist,
		Settings:  man.Settings,
		Source:    string(source),
		Timeout:   100 * time.Millisecond,
	}, m.logger)
	if err != nil {
		return nil, err
	}
	m.logf("info", "已加载插件: %s (%s)", man.ID, man.Name)
	return &loaded{manifest: man, dir: dir, plugin: p}, nil
}

// rebuildPipeline 用当前已加载插件重建管道(v1 管道仅含 JS 插件)。
func (m *Manager) rebuildPipeline() {
	m.pipe.Clear()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, l := range m.plugins {
		m.pipe.Register(l.plugin)
	}
}

// ---- api.PluginProvider 实现 ----

// ListPlugins 返回插件列表(供 UI 展示)。
func (m *Manager) ListPlugins() []map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]map[string]any, 0, len(m.plugins))
	for _, l := range m.plugins {
		out = append(out, map[string]any{
			"id":          l.manifest.ID,
			"name":        l.manifest.Name,
			"version":     l.manifest.Version,
			"description": l.manifest.Description,
			"author":      l.manifest.Author,
			"runtime":     l.manifest.Runtime,
			"enabled":     l.plugin.Enabled(),
			"priority":    l.manifest.Priority,
			"logs":        l.plugin.Logs(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["id"].(string) < out[j]["id"].(string)
	})
	return out
}

// EnablePlugin 启用/禁用插件并持久化。
func (m *Manager) EnablePlugin(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.plugins[id]
	if !ok {
		return os.ErrNotExist
	}
	l.plugin.SetEnabled(enabled)
	l.manifest.Enabled = enabled
	return saveManifest(l.dir, l.manifest)
}

// GetPluginSource 返回插件脚本源码。
func (m *Manager) GetPluginSource(id string) (string, bool) {
	m.mu.RLock()
	l, ok := m.plugins[id]
	m.mu.RUnlock()
	if !ok {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(l.dir, l.manifest.Entry))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// SavePluginSource 写入新源码并热重载该插件(保存即重载)。
func (m *Manager) SavePluginSource(id, source string) error {
	m.mu.Lock()
	l, ok := m.plugins[id]
	if !ok {
		m.mu.Unlock()
		return os.ErrNotExist
	}
	if err := os.WriteFile(filepath.Join(l.dir, l.manifest.Entry), []byte(source), 0o644); err != nil {
		m.mu.Unlock()
		return err
	}
	// 重建该插件实例。
	l.plugin.Close()
	nl, err := m.loadOne(l.dir)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.plugins[id] = nl
	m.mu.Unlock()

	m.rebuildPipeline()
	return nil
}

// Close 关闭所有插件。
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.plugins {
		l.plugin.Close()
	}
	m.plugins = make(map[string]*loaded)
}

func (m *Manager) logf(level, format string, args ...any) {
	if m.logger == nil {
		return
	}
	switch level {
	case "error":
		m.logger.Error(format, args...)
	case "info":
		m.logger.Info(format, args...)
	default:
		m.logger.Debug(format, args...)
	}
}
