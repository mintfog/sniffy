// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"sync"

	"github.com/mintfog/sniffy/internal/flow"
)

// defaultPageSize 是分页参数缺省/非法时的每页条数。
const defaultPageSize = 50

// pageBounds 把分页参数规整为 order 上的下标区间 [start, end)(按"最新优先"的倒序计数)。
// page/pageSize 来自 URL query,(page-1)*pageSize 可能溢出为负;溢出按越界页处理,返回空页。
func pageBounds(total, page, pageSize int) (start, end int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	start = (page - 1) * pageSize
	if start < 0 || start > total {
		start = total
	}
	end = start + pageSize
	if end < start || end > total {
		end = total
	}
	return start, end
}

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

// evictAll 在锁外逐个回调,通知调用方这些会话已不在存储中。
// 回调由调用方持锁取出后传入:onEvict 有并发读写,不能在锁外直接读该字段。
func evictAll(fn func(f *flow.Flow), flows []*flow.Flow) {
	if fn == nil {
		return
	}
	for _, f := range flows {
		if f != nil {
			fn(f)
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
	onEvict := s.onEvict
	s.mu.Unlock()
	evictAll(onEvict, evicted)
}

// setCap 调整容量上限并按需淘汰最旧记录(0 或负数忽略)。
func (s *sessionStore) setCap(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	s.cap = n
	evicted := s.trimLocked()
	onEvict := s.onEvict
	s.mu.Unlock()
	evictAll(onEvict, evicted)
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
	start, end := pageBounds(total, page, pageSize)
	out := make([]*flow.Flow, 0, end-start)
	// order 按入库顺序追加,倒序遍历即"最新在前";只遍历本页区间,不整表倒排。
	for i := total - 1 - start; i >= total-end; i-- {
		if f, ok := s.items[s.order[i]]; ok {
			out = append(out, f)
		}
	}
	return out, total
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
	onEvict := s.onEvict
	s.mu.Unlock()
	if ok {
		evictAll(onEvict, []*flow.Flow{f})
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
	onEvict := s.onEvict
	s.mu.Unlock()
	evictAll(onEvict, evicted)
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
	start, end := pageBounds(total, page, pageSize)
	out := make([]*flow.WSSession, 0, end-start)
	for i := total - 1 - start; i >= total-end; i-- {
		if ws, ok := s.items[s.order[i]]; ok {
			out = append(out, ws)
		}
	}
	return out, total
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
	start, end := pageBounds(total, page, pageSize)
	out := make([]*flow.StreamSession, 0, end-start)
	for i := total - 1 - start; i >= total-end; i-- {
		if ss, ok := s.items[s.order[i]]; ok {
			out = append(out, ss)
		}
	}
	return out, total
}

func (s *streamStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]*flow.StreamSession)
	s.order = nil
}
