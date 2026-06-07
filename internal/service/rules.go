// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// InterceptRule 与前端 web/src/types 的 InterceptRule 对齐。
type InterceptRule struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	Enabled       bool                 `json:"enabled"`
	Conditions    []InterceptCondition `json:"conditions"`
	Actions       []InterceptAction    `json:"actions"`
	Priority      int                  `json:"priority"`
	LogicOperator string               `json:"logicOperator"`
	Tags          []string             `json:"tags,omitempty"`
	CreatedAt     string               `json:"createdAt"`
	UpdatedAt     string               `json:"updatedAt"`
}

// InterceptCondition 匹配条件。
type InterceptCondition struct {
	Type          string `json:"type"`
	Operator      string `json:"operator"`
	Value         any    `json:"value"`
	Value2        any    `json:"value2,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	Negate        bool   `json:"negate,omitempty"`
	HeaderName    string `json:"headerName,omitempty"`
}

// InterceptAction 命中后的动作。
type InterceptAction struct {
	Type        string         `json:"type"`
	Parameters  map[string]any `json:"parameters"`
	Enabled     bool           `json:"enabled,omitempty"`
	Description string         `json:"description,omitempty"`
}

type ruleStore struct {
	mu    sync.RWMutex
	order []string
	items map[string]*InterceptRule
	path  string // 持久化文件;为空则仅内存
}

func newRuleStore(path string) *ruleStore {
	rs := &ruleStore{items: make(map[string]*InterceptRule), path: path}
	rs.load()
	return rs
}

func (rs *ruleStore) load() {
	if rs.path == "" {
		return
	}
	data, err := os.ReadFile(rs.path)
	if err != nil {
		return
	}
	var rules []*InterceptRule
	if json.Unmarshal(data, &rules) != nil {
		return
	}
	for _, r := range rules {
		rs.items[r.ID] = r
		rs.order = append(rs.order, r.ID)
	}
}

func (rs *ruleStore) save() {
	if rs.path == "" {
		return
	}
	out := make([]*InterceptRule, 0, len(rs.order))
	for _, id := range rs.order {
		if r, ok := rs.items[id]; ok {
			out = append(out, r)
		}
	}
	if data, err := json.MarshalIndent(out, "", "  "); err == nil {
		_ = os.WriteFile(rs.path, data, 0o644)
	}
}

func (rs *ruleStore) list() []*InterceptRule {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]*InterceptRule, 0, len(rs.order))
	for _, id := range rs.order {
		if r, ok := rs.items[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

func (rs *ruleStore) get(id string) (*InterceptRule, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	r, ok := rs.items[id]
	return r, ok
}

func (rs *ruleStore) create(r *InterceptRule) *InterceptRule {
	rs.mu.Lock()
	now := time.Now().Format(time.RFC3339)
	r.ID = "rule-" + flow.NewID()
	r.CreatedAt = now
	r.UpdatedAt = now
	if r.LogicOperator == "" {
		r.LogicOperator = "AND"
	}
	rs.items[r.ID] = r
	rs.order = append(rs.order, r.ID)
	rs.save()
	rs.mu.Unlock()
	return r
}

func (rs *ruleStore) update(id string, updated *InterceptRule) (*InterceptRule, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	existing, ok := rs.items[id]
	if !ok {
		return nil, false
	}
	updated.ID = id
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = time.Now().Format(time.RFC3339)
	rs.items[id] = updated
	rs.save()
	return updated, true
}

func (rs *ruleStore) toggle(id string, enabled bool) (*InterceptRule, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, ok := rs.items[id]
	if !ok {
		return nil, false
	}
	r.Enabled = enabled
	r.UpdatedAt = time.Now().Format(time.RFC3339)
	rs.save()
	return r, true
}

func (rs *ruleStore) delete(id string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, ok := rs.items[id]; ok {
		delete(rs.items, id)
		for i, oid := range rs.order {
			if oid == id {
				rs.order = append(rs.order[:i], rs.order[i+1:]...)
				break
			}
		}
		rs.save()
	}
}

func (rs *ruleStore) stats() InterceptStatsDTO {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	total := len(rs.items)
	active := 0
	for _, r := range rs.items {
		if r.Enabled {
			active++
		}
	}
	return InterceptStatsDTO{TotalRules: total, ActiveRules: active}
}

// InterceptStatsDTO 对应前端 InterceptStats。
type InterceptStatsDTO struct {
	TotalRules         int `json:"totalRules"`
	ActiveRules        int `json:"activeRules"`
	TotalInterceptions int `json:"totalInterceptions"`
	BlockedRequests    int `json:"blockedRequests"`
	ModifiedRequests   int `json:"modifiedRequests"`
	ModifiedResponses  int `json:"modifiedResponses"`
}
