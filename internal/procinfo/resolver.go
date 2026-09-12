// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package procinfo 根据连接地址解析进程信息，提供 TTL 缓存与检测超时保护。
// HTTP 和 WebSocket 处理器在独立 goroutine 中调用 Resolve，以隔离扫描和图标提取的耗时。
package procinfo

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/pkg/process"
)

const (
	defaultTTL      = 30 * time.Second
	defaultTimeout  = 2 * time.Second
	maxCacheEntries = 1024
)

// Resolver 解析并缓存连接对应的进程信息，通过 NewResolver 创建。
type Resolver struct {
	detector process.Detector
	icons    iconExtractor
	timeout  time.Duration
	ttl      time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type iconExtractor interface {
	ExtractIcon(string) (*process.ProcessIconInfo, error)
}

type cacheEntry struct {
	info *flow.ProcessInfo // nil 表示负缓存
	at   time.Time
}

// NewResolver 创建解析器。检测器创建失败时返回 nil，调用方据此跳过进程补全。
func NewResolver() *Resolver {
	detector, err := process.NewDetector()
	if err != nil || detector == nil {
		return nil
	}
	_ = detector.Start()
	return &Resolver{
		detector: detector,
		icons:    process.NewIconExtractor(),
		timeout:  defaultTimeout,
		ttl:      defaultTTL,
		cache:    make(map[string]cacheEntry),
	}
}

// Resolve 根据代理侧看到的客户端地址与代理监听地址解析发起进程。
//
// clientAddr 和 proxyAddr 分别取自代理接受连接的 RemoteAddr 和 LocalAddr。
// 检测器按客户端视角匹配 socket：本地端口为客户端临时端口，远端端口为代理端口。
// 无法定位进程或检测超时时返回 nil，结果均按客户端地址缓存。
func (r *Resolver) Resolve(clientAddr, proxyAddr net.Addr) *flow.ProcessInfo {
	if r == nil || r.detector == nil || clientAddr == nil {
		return nil
	}

	key := clientAddr.String()
	if info, ok := r.fromCache(key); ok {
		return info
	}

	info := r.lookup(clientAddr, proxyAddr)
	r.store(key, info)
	return info
}

func (r *Resolver) fromCache(key string) (*flow.ProcessInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.cache[key]; ok && time.Since(e.at) < r.ttl {
		return e.info, true
	}
	return nil, false
}

func (r *Resolver) store(key string, info *flow.ProcessInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 同一连接的并发查询可能在成功后才失败，保留仍有效的进程信息。
	if info == nil {
		if cached, ok := r.cache[key]; ok && cached.info != nil && time.Since(cached.at) < r.ttl {
			return
		}
	}
	// 客户端临时端口持续变化，容量上限用于约束缓存的内存占用。
	if len(r.cache) >= maxCacheEntries {
		r.cache = make(map[string]cacheEntry, maxCacheEntries)
	}
	r.cache[key] = cacheEntry{info: info, at: time.Now()}
}

// lookup 的超时限制等待检测结果的时间；超时后底层扫描仍会继续。
// 图标提取在收到检测结果后同步执行。
func (r *Resolver) lookup(clientAddr, proxyAddr net.Addr) *flow.ProcessInfo {
	// 预留一个发送位置，使检测器在调用方超时返回后仍能发送结果并退出。
	ch := make(chan *process.ProcessInfo, 1)
	go func() {
		pi, err := r.detector.GetProcessByConnection(clientAddr, proxyAddr)
		if err != nil {
			ch <- nil
			return
		}
		ch <- pi
	}()

	select {
	case pi := <-ch:
		if pi == nil {
			return nil
		}
		return r.toFlowProcess(pi)
	case <-time.After(r.timeout):
		return nil
	}
}

func (r *Resolver) toFlowProcess(pi *process.ProcessInfo) *flow.ProcessInfo {
	fp := &flow.ProcessInfo{
		PID:  pi.PID,
		Name: pi.Name,
		Path: pi.Path,
		User: pi.User,
	}
	if r.icons == nil {
		return fp
	}
	icon, err := r.icons.ExtractIcon(pi.Path)
	if err != nil || icon == nil {
		return fp
	}
	fp.HasIcon = icon.HasIcon
	fp.IconData = icon.IconData
	fp.IconType = icon.IconType
	fp.IconCategory = icon.IconCategory
	fp.IconSize = parseIconSize(icon.IconSize)
	return fp
}

// parseIconSize 从“宽x高”或“宽”中提取整数宽度；宽度解析失败时返回 0。
func parseIconSize(s string) int {
	if s == "" {
		return 0
	}
	if i := strings.IndexByte(s, 'x'); i > 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
