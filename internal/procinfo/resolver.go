// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package procinfo 把抓到的连接异步解析成进程信息(名/PID/路径/图标),
// 供 HTTP / WebSocket 处理器在不阻塞热路径的前提下补全 flow 的进程信息。
//
// 解析底层复用 pkg/process 的平台检测器与图标提取器,并在此层叠加:
//   - 按客户端地址的 TTL 缓存(含负缓存),避免对每个请求重复扫描 /proc;
//   - 单次解析的超时保护,防止慢扫描拖住补全 goroutine。
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

// Resolver 解析连接对应的进程信息。零值不可用,请用 NewResolver。
type Resolver struct {
	detector process.Detector
	icons    *process.IconExtractor
	timeout  time.Duration
	ttl      time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	info *flow.ProcessInfo // 可能为 nil(负缓存)
	at   time.Time
}

// NewResolver 创建解析器。检测器创建失败时返回 nil(调用方据此跳过进程补全)。
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
// 语义:clientAddr 是代理 accept 到的对端地址(即 conn.RemoteAddr,客户端临时端口),
// proxyAddr 是代理本地监听地址(即 conn.LocalAddr)。据此在 /proc 等处匹配
// "本地端口=客户端临时端口、远端端口=代理端口" 的那条 socket,从而定位客户端进程。
// 无法解析(非本机客户端 / 超时 / 权限不足)时返回 nil。
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
	// 简单的容量上限:超出则整体清空(代价是偶发缓存抖动,可接受)。
	if len(r.cache) >= maxCacheEntries {
		r.cache = make(map[string]cacheEntry, maxCacheEntries)
	}
	r.cache[key] = cacheEntry{info: info, at: time.Now()}
}

// lookup 在超时保护下调用平台检测器,并映射为 flow.ProcessInfo。
func (r *Resolver) lookup(clientAddr, proxyAddr net.Addr) *flow.ProcessInfo {
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

// toFlowProcess 把 pkg/process 的进程信息映射为 flow.ProcessInfo,并补充图标。
func (r *Resolver) toFlowProcess(pi *process.ProcessInfo) *flow.ProcessInfo {
	fp := &flow.ProcessInfo{
		PID:  pi.PID,
		Name: pi.Name,
		Path: pi.Path,
		User: pi.User,
	}
	if r.icons != nil {
		if icon, err := r.icons.ExtractIcon(pi.Path); err == nil && icon != nil {
			fp.HasIcon = icon.HasIcon
			fp.IconData = icon.IconData
			fp.IconType = icon.IconType
			fp.IconCategory = icon.IconCategory
			fp.IconSize = parseIconSize(icon.IconSize)
		}
	}
	return fp
}

// parseIconSize 把 "32x32" 形式的尺寸解析为像素宽度;无法解析时返回 0。
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
