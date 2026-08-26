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

// breakRuleFileName 是 URL 断点规则的持久化文件名。
//
// 单独一份而不是塞进 config.json:后者会随 /api/config 的任意一次写入整体回存,
// 把一份「什么时候被谁改的」说不清的规则集合混进用户偏好里。
const breakRuleFileName = "breakpoints.json"

// BreakRuleSpec 是一条可持久化的 URL 断点规则。字段与 pipeline.BreakRule 的 JSON 形状
// 逐字对齐,但刻意另立一份类型:service 只管落盘与 schema,不反向依赖 pipeline
// (装配层负责两者之间的转换)。
type BreakRuleSpec struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	OnRequest  bool   `json:"onRequest"`
	OnResponse bool   `json:"onResponse"`
	Enabled    bool   `json:"enabled"`
}

type breakRuleStore struct {
	mu    sync.RWMutex
	items []BreakRuleSpec
	path  string // 持久化文件;为空则仅内存
}

func newBreakRuleStore(path string) *breakRuleStore {
	s := &breakRuleStore{path: path}
	s.load()
	return s
}

// load 读取已落盘的规则。文件缺失或内容坏掉都按空集起步:一份读不懂的规则文件不该
// 拦住整个程序启动,而断点规则为空只是"这次没有断点",不会误伤流量。
func (s *breakRuleStore) load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var items []BreakRuleSpec
	if json.Unmarshal(data, &items) != nil {
		return
	}
	s.items = items
}

func (s *breakRuleStore) list() []BreakRuleSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]BreakRuleSpec(nil), s.items...)
}

// save 整体替换并原子落盘:先写同目录 temp(0600)再 rename 覆盖。
// 规则是用户逐条敲进去的,写到一半崩溃损坏文件等于把整批规则一起丢掉。
func (s *breakRuleStore) save(items []BreakRuleSpec) error {
	s.mu.Lock()
	s.items = append([]BreakRuleSpec(nil), items...)
	path := s.path
	data, err := json.MarshalIndent(s.items, "", "  ")
	s.mu.Unlock()
	if err != nil || path == "" {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// BreakRules 返回已持久化的 URL 断点规则,供装配层在启动时灌回断点管理器。
func (s *Service) BreakRules() []BreakRuleSpec { return s.breakRules.list() }

// SaveBreakRules 整体覆盖持久化的 URL 断点规则。
//
// 只持久化 URL 规则,不持久化全局"断在请求/响应"开关:后者是把全部流量按住的模态状态,
// 恢复它意味着 App 一启动就在用户看到界面之前静默冻结所有流量,而它重新打开只要一次点击。
func (s *Service) SaveBreakRules(rules []BreakRuleSpec) error { return s.breakRules.save(rules) }
