// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// Emitter 把插件事件(如实时日志)广播到上层(由装配层接到事件总线)。可为 nil。
type Emitter func(eventType string, payload any)

// idPattern 限定插件 ID 为文件系统安全的短标识,杜绝路径穿越。
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

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
	emit   Emitter

	// opMu 串行化所有「写」操作(Load/Create/Delete/Save/Update/Enable),消除其相互交错
	// 的窗口(并发同 id 创建泄漏实例、删除后被 swap 复活、并发保存丢更新等)。
	// mu 仅保护 plugins/failed 这两张表,使 ListPlugins/GetPluginSource 等「读」不被长耗时
	// 的实例构建阻塞。两把锁的获取顺序恒为 opMu → mu,故无死锁。
	opMu sync.Mutex

	mu      sync.RWMutex
	plugins map[string]*loaded
	failed  map[string]string // 加载失败的插件目录名 -> 错误信息(供 UI 显示)
}

// NewManager 创建插件管理器。dir 为用户插件根目录;emit 用于实时日志广播,可为 nil。
func NewManager(pipe *pipeline.Pipeline, dir string, logger Logger, emit Emitter) *Manager {
	return &Manager{
		pipe:    pipe,
		dir:     dir,
		logger:  logger,
		emit:    emit,
		plugins: make(map[string]*loaded),
		failed:  make(map[string]string),
	}
}

// LoadAll 扫描插件目录,加载全部插件并重建管道。
func (m *Manager) LoadAll() error {
	if m.dir == "" {
		return nil
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
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
	m.failed = make(map[string]string)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.dir, e.Name())
		if l, err := m.loadOne(dir, nil); err != nil {
			m.failed[e.Name()] = err.Error()
			m.logf("error", "加载插件失败 %s: %v", e.Name(), err)
		} else {
			m.plugins[l.manifest.ID] = l
		}
	}
	m.mu.Unlock()

	m.rebuildPipeline()
	return nil
}

// loadOne 从磁盘读取 manifest + 入口脚本并构建插件实例。
// initialStore 非 nil 时(热重载)迁移上个实例的 store。
func (m *Manager) loadOne(dir string, initialStore map[string]any) (*loaded, error) {
	man, err := loadManifest(dir)
	if err != nil {
		return nil, err
	}
	source, err := os.ReadFile(filepath.Join(dir, man.Entry))
	if err != nil {
		return nil, err
	}
	p, err := m.buildPlugin(dir, man, string(source), initialStore)
	if err != nil {
		return nil, err
	}
	m.logf("info", "已加载插件: %s (%s)", man.ID, man.Name)
	return &loaded{manifest: man, dir: dir, plugin: p}, nil
}

// buildPlugin 用给定 manifest + 源码构建一个 JS 插件(不触碰磁盘上的源码文件)。
func (m *Manager) buildPlugin(dir string, man Manifest, source string, initialStore map[string]any) (*js.Plugin, error) {
	id := man.ID
	return js.NewPlugin(js.Config{
		ID:           man.ID,
		Name:         man.Name,
		Priority:     man.Priority,
		Enabled:      man.Enabled,
		Whitelist:    man.Whitelist,
		Blacklist:    man.Blacklist,
		Settings:     man.Settings,
		Source:       source,
		Timeout:      100 * time.Millisecond,
		StatePath:    filepath.Join(dir, "state.json"),
		InitialStore: initialStore,
		OnLog: func(e js.LogEntry) {
			if m.emit != nil {
				m.emit("plugin_log", map[string]any{"id": id, "level": e.Level, "msg": e.Msg, "time": e.Time})
			}
		},
	}, m.logger)
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

// ListPlugins 返回插件列表(供 UI 展示),含已加载与加载失败的条目。
func (m *Manager) ListPlugins() []map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]map[string]any, 0, len(m.plugins)+len(m.failed))
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
			"whitelist":   l.manifest.Whitelist,
			"blacklist":   l.manifest.Blacklist,
			"settings":    l.manifest.Settings,
			"logs":        l.plugin.Logs(),
		})
	}
	// 加载失败的插件也回报,带 error 字段,使作者能看到原因而非凭空消失。
	for name, errMsg := range m.failed {
		out = append(out, map[string]any{
			"id":      name,
			"name":    name,
			"enabled": false,
			"error":   errMsg,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["id"].(string) < out[j]["id"].(string)
	})
	return out
}

// EnablePlugin 启用/禁用插件并持久化。
func (m *Manager) EnablePlugin(id string, enabled bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
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
// 原子语义:先用新源码构建实例(编译/求值),失败则磁盘源码与旧实例均不动。
func (m *Manager) SavePluginSource(id, source string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	l, ok := m.plugins[id]
	m.mu.RUnlock()
	if !ok {
		return os.ErrNotExist
	}
	dir, man := l.dir, l.manifest
	np, err := m.buildPlugin(dir, man, source, l.plugin.Snapshot())
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, man.Entry), []byte(source), 0o644); err != nil {
		np.Close()
		return err
	}
	m.swap(id, &loaded{manifest: man, dir: dir, plugin: np})
	return nil
}

// CreatePlugin 在插件目录下新建一个插件(目录 + manifest + 入口脚本)并加载。
// 源码无法编译时清理已创建目录,保证创建原子。meta 为前端传入的 manifest 字段。
func (m *Manager) CreatePlugin(meta map[string]any, source string) (map[string]any, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	man := manifestFromMap(meta)
	if !idPattern.MatchString(man.ID) {
		return nil, fmt.Errorf("非法插件 ID(仅允许小写字母/数字/_/-,1-64 字符): %q", man.ID)
	}
	if man.Entry == "" {
		man.Entry = "index.js"
	}
	if man.Runtime == "" {
		man.Runtime = "js"
	}
	if man.Priority == 0 {
		man.Priority = 100
	}
	if man.Version == "" {
		man.Version = "1.0.0"
	}
	if source == "" {
		source = newPluginTemplate
	}

	m.mu.RLock()
	_, exists := m.plugins[man.ID]
	m.mu.RUnlock()
	dir := filepath.Join(m.dir, man.ID)
	if exists || dirExists(dir) {
		return nil, fmt.Errorf("插件 ID 已存在: %s", man.ID)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := saveManifest(dir, man); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, man.Entry), []byte(source), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	np, err := m.buildPlugin(dir, man, source, nil)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	m.mu.Lock()
	m.plugins[man.ID] = &loaded{manifest: man, dir: dir, plugin: np}
	delete(m.failed, man.ID)
	m.mu.Unlock()
	m.rebuildPipeline()
	return manifestToMap(man), nil
}

// DeletePlugin 关闭并删除一个插件(实例 + 磁盘目录)。
func (m *Manager) DeletePlugin(id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	l, ok := m.plugins[id]
	if ok {
		delete(m.plugins, id)
	}
	// 兼容删除「加载失败」的目录条目。
	failDir := ""
	if !ok {
		if _, isFailed := m.failed[id]; isFailed {
			failDir = filepath.Join(m.dir, id)
			delete(m.failed, id)
		}
	}
	m.mu.Unlock()

	if ok {
		l.plugin.Close()
		if err := os.RemoveAll(l.dir); err != nil {
			return err
		}
		m.rebuildPipeline()
		return nil
	}
	if failDir != "" {
		return os.RemoveAll(failDir)
	}
	return os.ErrNotExist
}

// UpdateManifest 更新插件 manifest 的可编辑字段并热重载(优先级/白黑名单/settings 需重建实例才能生效)。
func (m *Manager) UpdateManifest(id string, patch map[string]any) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	l, ok := m.plugins[id]
	m.mu.RUnlock()
	if !ok {
		return os.ErrNotExist
	}
	p := manifestFromMap(patch)
	man := l.manifest
	man.Name = p.Name
	man.Version = p.Version
	man.Author = p.Author
	man.Description = p.Description
	man.Priority = p.Priority
	if man.Priority == 0 {
		man.Priority = 100
	}
	man.Whitelist = p.Whitelist
	man.Blacklist = p.Blacklist
	man.Settings = p.Settings

	source, err := os.ReadFile(filepath.Join(l.dir, man.Entry))
	if err != nil {
		return err
	}
	np, err := m.buildPlugin(l.dir, man, string(source), l.plugin.Snapshot())
	if err != nil {
		return err
	}
	if err := saveManifest(l.dir, man); err != nil {
		np.Close()
		return err
	}
	m.swap(id, &loaded{manifest: man, dir: l.dir, plugin: np})
	return nil
}

// ClearPluginLogs 清空指定插件的日志缓冲。
func (m *Manager) ClearPluginLogs(id string) error {
	m.mu.RLock()
	l, ok := m.plugins[id]
	m.mu.RUnlock()
	if !ok {
		return os.ErrNotExist
	}
	l.plugin.ClearLogs()
	return nil
}

// swap 以新实例替换已加载插件,关闭旧实例并重建管道。
func (m *Manager) swap(id string, nl *loaded) {
	m.mu.Lock()
	if cur, ok := m.plugins[id]; ok {
		cur.plugin.Close()
	}
	m.plugins[id] = nl
	m.mu.Unlock()
	m.rebuildPipeline()
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
