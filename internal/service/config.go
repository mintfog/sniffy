// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// configFileName 持久化配置在 configDir 下的文件名。
const configFileName = "config.json"

// AppConfig 对应前端 SniffyConfig 的核心字段(可持久化)。
type AppConfig struct {
	Port         int    `json:"port"`
	EnableHTTPS  bool   `json:"enableHTTPS"`
	Recording    bool   `json:"recording"`
	MaxFlows     int    `json:"maxFlows,omitempty"` // 会话存储容量上限;0 取默认值
	Upstream     bool   `json:"upstream"`           // 是否启用上游(二级)代理
	UpstreamAddr string `json:"upstreamAddr"`       // 上游代理地址,如 http://host:port
	SystemProxy  bool   `json:"systemProxy"`        // 是否把本机系统代理指向 Sniffy 监听端口
	AutoProxy    bool   `json:"autoSystemProxy"`    // 是否在每次启动时自动开启系统代理
	// RunInBackground 决定关闭主窗口的行为:true 隐藏到托盘保持后台运行(经托盘再打开),
	// false 则关闭 = 完全退出。仅桌面 transport 参考,headless 忽略。
	RunInBackground bool `json:"runInBackground"`
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
	// 以当前默认值为底解码,文件中缺失的字段保持默认而不是被清零。
	c := cs.cfg
	if readConfigFile(cs.path, &c) {
		cs.cfg = c
	}
}

// readConfigFile 把 path 处的 JSON 配置解码到 into,成功返回 true。
func readConfigFile(path string, into *AppConfig) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, into) == nil
}

// LoadSaved 读取 configDir 下持久化的 config.json。
// 文件不存在或解析失败时 ok 为 false。供装配层在引擎创建前取回上次保存的配置。
func LoadSaved(configDir string) (AppConfig, bool) {
	if configDir == "" {
		return AppConfig{}, false
	}
	var c AppConfig
	if !readConfigFile(filepath.Join(configDir, configFileName), &c) {
		return AppConfig{}, false
	}
	return c, true
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

// setSystemProxy 仅更新并持久化系统代理当前开关,不触发任何应用动作。
// 供桌面装配层在启动时把状态对齐为「自动启用」的结果。
func (cs *configStore) setSystemProxy(on bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.cfg.SystemProxy = on
	cs.save()
}

// update 合并部分字段并持久化。
//
// 监听端口(port)允许前端修改并持久化:它是启动期确定的部署设置(默认值 <
// config.json < headless 命令行参数),运行时改了不会即时重新绑定,需重启后才生效
// (见 app.ResolveListen)。监听地址(host)固定由默认值/命令行参数决定,不接受 IPC 修改。
func (cs *configStore) update(patch map[string]any) AppConfig {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if v, ok := patch["port"].(float64); ok && int(v) >= 1 && int(v) <= 65535 {
		cs.cfg.Port = int(v)
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
	if v, ok := patch["systemProxy"].(bool); ok {
		cs.cfg.SystemProxy = v
	}
	if v, ok := patch["autoSystemProxy"].(bool); ok {
		cs.cfg.AutoProxy = v
	}
	if v, ok := patch["runInBackground"].(bool); ok {
		cs.cfg.RunInBackground = v
	}
	cs.save()
	return cs.cfg
}
