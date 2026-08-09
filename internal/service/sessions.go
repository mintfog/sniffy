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
	// onEvict 在一条会话被淘汰 / 删除 / 清空时调用,用于回收它的响应体落盘副本。
	// 一律在释放锁之后调用(删文件是 IO,不该压在存储锁里)。
	onEvict func(f *flow.Flow)
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

// setOnEvict 注册会话被移出存储时的回收回调。
func (s *sessionStore) setOnEvict(fn func(f *flow.Flow)) {
	s.mu.Lock()
	s.onEvict = fn
	s.mu.Unlock()
}

// evict 在锁外逐个回调,通知调用方这些会话已不在存储中。
func (s *sessionStore) evict(flows []*flow.Flow) {
	if s.onEvict == nil {
		return
	}
	for _, f := range flows {
		if f != nil {
			s.onEvict(f)
		}
	}
}

func (s *sessionStore) put(f *flow.Flow) {
	s.mu.Lock()
	var evicted []*flow.Flow
	if _, exists := s.items[f.ID]; !exists {
		s.order = append(s.order, f.ID)
		// 超出容量时淘汰最旧的。
		evicted = s.trimLocked()
	}
	s.items[f.ID] = f
	s.mu.Unlock()
	s.evict(evicted)
}

// setCap 调整容量上限并按需淘汰最旧记录(0 或负数忽略)。
func (s *sessionStore) setCap(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	s.cap = n
	evicted := s.trimLocked()
	s.mu.Unlock()
	s.evict(evicted)
}

// trimLocked 把超出容量的最旧记录摘出来,返回被淘汰的 Flow(供锁外回收其落盘副本)。
func (s *sessionStore) trimLocked() []*flow.Flow {
	var evicted []*flow.Flow
	for len(s.order) > s.cap {
		oldest := s.order[0]
		s.order = s.order[1:]
		if f, ok := s.items[oldest]; ok {
			evicted = append(evicted, f)
		}
		delete(s.items, oldest)
	}
	return evicted
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
	f, ok := s.items[id]
	if ok {
		delete(s.items, id)
		for i, oid := range s.order {
			if oid == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
	s.mu.Unlock()
	if ok {
		s.evict([]*flow.Flow{f})
	}
}

func (s *sessionStore) clear() {
	s.mu.Lock()
	evicted := make([]*flow.Flow, 0, len(s.items))
	for _, f := range s.items {
		evicted = append(evicted, f)
	}
	s.items = make(map[string]*flow.Flow)
	s.order = nil
	s.mu.Unlock()
	s.evict(evicted)
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

// streamStore 存储流式会话(SSE / gRPC / 分块流),结构同 wsStore。
type streamStore struct {
	mu    sync.RWMutex
	order []string
	items map[string]*flow.StreamSession
	cap   int
}

func newStreamStore(capacity int) *streamStore {
	if capacity <= 0 {
		capacity = 2000
	}
	return &streamStore{items: make(map[string]*flow.StreamSession), cap: capacity}
}

func (s *streamStore) put(ss *flow.StreamSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[ss.ID]; !exists {
		s.order = append(s.order, ss.ID)
		for len(s.order) > s.cap {
			oldest := s.order[0]
			s.order = s.order[1:]
			delete(s.items, oldest)
		}
	}
	s.items[ss.ID] = ss
}

func (s *streamStore) get(id string) (*flow.StreamSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ss, ok := s.items[id]
	return ss, ok
}

func (s *streamStore) list(page, pageSize int) ([]*flow.StreamSession, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.order)
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	rev := make([]*flow.StreamSession, 0, total)
	for i := total - 1; i >= 0; i-- {
		if ss, ok := s.items[s.order[i]]; ok {
			rev = append(rev, ss)
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

func (s *streamStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]*flow.StreamSession)
	s.order = nil
}
