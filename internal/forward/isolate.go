// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package forward

import (
	"context"
	"net/http"
	"net/url"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/mintfog/sniffy/capture/types"
)

// maxIsolatedPools 是同时保留的原始目标连接池个数，超出时淘汰最久未用的。
const maxIsolatedPools = 256

// isolatedTransport 让透明连接的请求按原始目标各用一个连接池。
//
// http.Transport 的连接池（含 HTTP/2）只按域名取连接：同一域名先后连向两个不同原始目标的
// 请求会共用先建好的那条连接，后一个请求就发到了前一个目标上。连接池的键无从扩展，
// 故每个原始目标各持一份 base 的克隆。
type isolatedTransport struct {
	base *http.Transport

	mu    sync.Mutex
	pools *lru.Cache[string, *http.Transport]
}

// IsolateByOriginalDst 包装 base：透明连接上直连原始目标的请求改由该目标专属的克隆发出，
// 其余请求仍走 base。base 的配置须在首个请求之前定好，克隆只取当时的快照。
func IsolateByOriginalDst(base *http.Transport) http.RoundTripper {
	pools, _ := lru.NewWithEvict(maxIsolatedPools, func(_ string, pool *http.Transport) {
		pool.CloseIdleConnections()
	})
	return &isolatedTransport{base: base, pools: pools}
}

func (t *isolatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if !types.IsTransparent(ctx) {
		return t.base.RoundTrip(req)
	}
	if originalDst, ok := pinnedOriginalDst(ctx, req.URL); ok && !t.viaProxy(req) {
		return t.pool(originalDst).RoundTrip(req)
	}
	// base 拨的是上游代理或被改写后的目标，都不该换成原始目标。
	return t.base.RoundTrip(req.WithContext(types.WithoutDialPin(ctx)))
}

// pinnedOriginalDst 返回直连 u 时实际要拨的原始目标。ctx 不属于透明连接，或 u 已不是请求
// 原本的 authority（被插件改写）时 ok 为 false，按 u 自身拨号。
func pinnedOriginalDst(ctx context.Context, u *url.URL) (originalDst string, ok bool) {
	if !types.IsTransparent(ctx) {
		return "", false
	}
	target := canonicalAddr(u)
	pinned := types.PinnedDialAddr(ctx, target)
	return pinned, pinned != target
}

func (t *isolatedTransport) viaProxy(req *http.Request) bool {
	if t.base.Proxy == nil {
		return false
	}
	proxyURL, err := t.base.Proxy(req)
	return err == nil && proxyURL != nil
}

func (t *isolatedTransport) pool(originalDst string) *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pool, ok := t.pools.Get(originalDst); ok {
		return pool
	}
	pool := t.base.Clone()
	// 选定克隆即已决定直连原始目标；上游代理在此之后才配上时，这个请求也不改走代理。
	pool.Proxy = nil
	t.pools.Add(originalDst, pool)
	return pool
}

func (t *isolatedTransport) CloseIdleConnections() {
	t.base.CloseIdleConnections()
	t.mu.Lock()
	pools := t.pools.Values()
	t.mu.Unlock()
	for _, pool := range pools {
		pool.CloseIdleConnections()
	}
}
