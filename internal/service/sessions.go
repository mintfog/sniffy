// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"sync"

	"github.com/mintfog/sniffy/internal/flow"
)

// sessionStore 按 flow.ID 键控存储 HTTP 会话(即 Flow),带容量上限的有序环。
type sessionStore struct {
	mu    sync.RWMutex
	order []string
	items map[string]*flow.Flow
	cap   int
}

func newSessionStore(capacity int) *sessionStore {
	if capacity <= 0 {
		capacity = 5000
	}
	return &sessionStore{
		items: make(map[string]*flow.Flow),
		cap:   capacity,
	}
}

func (s *sessionStore) put(f *flow.Flow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[f.ID]; !exists {
		s.order = append(s.order, f.ID)
		// 超出容量时淘汰最旧的。
		for len(s.order) > s.cap {
			oldest := s.order[0]
			s.order = s.order[1:]
			delete(s.items, oldest)
		}
	}
	s.items[f.ID] = f
}

// setCap 调整容量上限并按需淘汰最旧记录(0 或负数忽略)。
func (s *sessionStore) setCap(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cap = n
	for len(s.order) > s.cap {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.items, oldest)
	}
}

func (s *sessionStore) get(id string) (*flow.Flow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.items[id]
	return f, ok
}

// list 返回最新优先的分页结果与总数。
func (s *sessionStore) list(page, pageSize int) ([]*flow.Flow, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.order)
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	// 倒序(最新在前)。
	rev := make([]*flow.Flow, 0, total)
	for i := total - 1; i >= 0; i-- {
		if f, ok := s.items[s.order[i]]; ok {
			rev = append(rev, f)
		}
	}
	start := (page - 1) * pageSize
	if start > len(rev) {
		start = len(rev)
	}
	end := start + pageSize
	if end > len(rev) {
		end = len(rev)
	}
	return rev[start:end], total
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; ok {
		delete(s.items, id)
		for i, oid := range s.order {
			if oid == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
}

func (s *sessionStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]*flow.Flow)
	s.order = nil
}

// wsStore 存储 WebSocket 会话。
type wsStore struct {
	mu    sync.RWMutex
	order []string
	items map[string]*flow.WSSession
	cap   int
}

func newWSStore(capacity int) *wsStore {
	if capacity <= 0 {
		capacity = 2000
	}
	return &wsStore{items: make(map[string]*flow.WSSession), cap: capacity}
}

func (s *wsStore) put(ws *flow.WSSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[ws.ID]; !exists {
		s.order = append(s.order, ws.ID)
		for len(s.order) > s.cap {
			oldest := s.order[0]
			s.order = s.order[1:]
			delete(s.items, oldest)
		}
	}
	s.items[ws.ID] = ws
}

func (s *wsStore) get(id string) (*flow.WSSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ws, ok := s.items[id]
	return ws, ok
}

func (s *wsStore) list(page, pageSize int) ([]*flow.WSSession, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.order)
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	rev := make([]*flow.WSSession, 0, total)
	for i := total - 1; i >= 0; i-- {
		if ws, ok := s.items[s.order[i]]; ok {
			rev = append(rev, ws)
		}
	}
	start := (page - 1) * pageSize
	if start > len(rev) {
		start = len(rev)
	}
	end := start + pageSize
	if end > len(rev) {
		end = len(rev)
	}
	return rev[start:end], total
}

func (s *wsStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]*flow.WSSession)
	s.order = nil
}
