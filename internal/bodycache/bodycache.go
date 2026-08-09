// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package bodycache 管理「大体积响应体」的落盘副本。
//
// 走透传旁路的响应体不进内存(见 capture 的 passthrough),转发的同时在这里留一份,
// 供详情页的预览与「另存为」按需读取。副本按缓存对待:进程启动时清空,超出容量按最旧
// 淘汰;副本不在时只是这两个功能取不到内容。
//
// 写入是 best-effort:落盘出错(磁盘满 / 权限)只让这条记录失去副本,不打断转发。
package bodycache

import (
	"os"
	"path/filepath"
	"sync"
)

// DefaultBudget 是缓存目录的默认容量上限。
const DefaultBudget int64 = 2 << 30 // 2GiB

// Cache 是一个带容量上限的落盘副本目录。零值不可用,须经 New 构造;
// *Cache 为 nil 时所有方法退化为空操作(未启用旁路 / 独立测试)。
type Cache struct {
	dir    string
	budget int64

	mu    sync.Mutex
	order []string         // 已提交副本的提交顺序,淘汰时从头取
	sizes map[string]int64 // 路径 → 字节数
	total int64
}

// New 打开缓存目录并清空其中的残留副本(它们对应的会话不会跨进程存活),
// budget<=0 时取 DefaultBudget。
func New(dir string, budget int64) (*Cache, error) {
	if budget <= 0 {
		budget = DefaultBudget
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return &Cache{dir: dir, budget: budget, sizes: make(map[string]int64)}, nil
}

// Dir 返回缓存目录。
func (c *Cache) Dir() string {
	if c == nil {
		return ""
	}
	return c.dir
}

// SetBudget 调整容量上限并立即按需淘汰(<=0 忽略)。
func (c *Cache) SetBudget(budget int64) {
	if c == nil || budget <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.budget = budget
	c.evictLocked()
}

// Create 为一条 flow 开一个待写入的副本。返回的 *Entry 可为 nil(缓存未启用或建文件
// 失败),其方法对 nil 安全,调用方无需分支判断。
func (c *Cache) Create(flowID string) *Entry {
	if c == nil || flowID == "" {
		return nil
	}
	path := filepath.Join(c.dir, flowID+".body")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return &Entry{c: c, f: f, path: path}
}

// Remove 删除一份副本(会话被淘汰 / 删除时调用)。path 为空或不属于本缓存时忽略。
func (c *Cache) Remove(path string) {
	if c == nil || path == "" {
		return
	}
	c.mu.Lock()
	c.forgetLocked(path)
	c.mu.Unlock()
	_ = os.Remove(path)
}

// Clear 删除全部副本(清空会话时调用)。
func (c *Cache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	paths := c.order
	c.order = nil
	c.sizes = make(map[string]int64)
	c.total = 0
	c.mu.Unlock()
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// Total 返回当前已提交副本占用的字节数(测试与诊断用)。
func (c *Cache) Total() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// commit 登记一份写完的副本,并在超出容量时淘汰最旧的若干份。
func (c *Cache) commit(path string, size int64) {
	c.mu.Lock()
	if _, exists := c.sizes[path]; !exists {
		c.order = append(c.order, path)
	} else {
		c.total -= c.sizes[path]
	}
	c.sizes[path] = size
	c.total += size
	stale := c.evictLocked()
	c.mu.Unlock()
	for _, p := range stale {
		_ = os.Remove(p)
	}
}

// evictLocked 淘汰最旧的副本直到总量回到上限内,返回待删除的路径(在锁外删)。
func (c *Cache) evictLocked() []string {
	var stale []string
	for c.total > c.budget && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		c.total -= c.sizes[oldest]
		delete(c.sizes, oldest)
		stale = append(stale, oldest)
	}
	return stale
}

// forgetLocked 把一条路径从记账中摘掉(不删文件)。
func (c *Cache) forgetLocked(path string) {
	size, ok := c.sizes[path]
	if !ok {
		return
	}
	delete(c.sizes, path)
	c.total -= size
	for i, p := range c.order {
		if p == path {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// Entry 是一份正在写入的副本。它只被单个转发 goroutine 使用,故无需加锁。
type Entry struct {
	c      *Cache
	f      *os.File
	path   string
	n      int64
	failed bool
}

// Write 追加字节,且永不返错:调用方把它挂在 io.MultiWriter 上与客户端那一路并列,
// 返错会连带中断整个拷贝。落盘出错时记下失败、丢掉这份副本并静默吞掉后续写入。
func (e *Entry) Write(p []byte) (int, error) {
	if e == nil || e.failed {
		return len(p), nil
	}
	n, err := e.f.Write(p)
	e.n += int64(n)
	if err != nil {
		e.failed = true
		_ = e.f.Close()
		_ = os.Remove(e.path)
	}
	return len(p), nil
}

// Commit 收尾并登记副本,返回路径与字节数;缓存未启用或写入失败时返回空路径。
func (e *Entry) Commit() (string, int64) {
	if e == nil {
		return "", 0
	}
	if e.failed {
		return "", 0
	}
	if err := e.f.Close(); err != nil {
		_ = os.Remove(e.path)
		return "", 0
	}
	e.c.commit(e.path, e.n)
	return e.path, e.n
}

// Abort 放弃这份副本(转发中途出错时调用)。
func (e *Entry) Abort() {
	if e == nil || e.failed {
		return
	}
	e.failed = true
	_ = e.f.Close()
	_ = os.Remove(e.path)
}
