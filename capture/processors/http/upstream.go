// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
)

// UpstreamProxy 是一个上游代理地址，以及它是否就在本机。两者一起换，读取方不会拿到一新一旧。
type UpstreamProxy struct {
	url *url.URL
	// local 为真时代理是本机上的程序（如 mihomo、v2rayN 的本地端口）。开着虚拟网卡时它自己的
	// 出站也会进虚拟网卡、回到 sniffy，透明连接再交给它，就一层层转回自己，直到耗尽连接与内存。
	local bool
	// unresolved 为真时 local 是解析域名失败后的推定，见 ResolveUpstreamProxy。
	unresolved bool
}

// NewUpstreamProxy 复制 u 并判定它是否在本机；u 为 nil 时返回 nil（直连）。
//
// 本机指回环、未指定地址、localhost，以及此刻任一网卡上的地址；其余域名按不在本机处理，不为此
// 解析（要解析的用 ResolveUpstreamProxy）。网卡地址取的是此刻的快照，之后变了要重新判定。
func NewUpstreamProxy(u *url.URL) *UpstreamProxy {
	if u == nil {
		return nil
	}
	cp := *u
	return &UpstreamProxy{url: &cp, local: !upstreamAlwaysRemote.Load() && isLocalHost(cp.Hostname())}
}

// ResolveUpstreamProxy 与 NewUpstreamProxy 相同，只是地址为域名时还用 lookup 解析它，任一结果在本机
// 即算在本机。解析失败时按在本机处理并返回错误：误判为不在本机，开着虚拟网卡时就会形成回环（见
// UpstreamProxy.local）；误判为在本机，代价只是经虚拟网卡的连接不交给它。
func ResolveUpstreamProxy(ctx context.Context, u *url.URL, lookup func(context.Context, string) ([]netip.Addr, error)) (*UpstreamProxy, error) {
	p := NewUpstreamProxy(u)
	if !p.needsLookup() {
		return p, nil
	}
	addrs, err := lookup(ctx, p.url.Hostname())
	p.unresolved = err != nil
	p.local = p.unresolved || slices.ContainsFunc(addrs, isLocalAddr)
	return p, err
}

// UpstreamNeedsLookup 报告 ResolveUpstreamProxy 判定 u 是否在本机时要不要解析域名。
func UpstreamNeedsLookup(u *url.URL) bool { return NewUpstreamProxy(u).needsLookup() }

// needsLookup 报告 NewUpstreamProxy 给出的 p 还要不要解析域名才能判定是否在本机。
func (p *UpstreamProxy) needsLookup() bool {
	if p == nil || p.local || upstreamAlwaysRemote.Load() {
		return false
	}
	_, err := netip.ParseAddr(p.url.Hostname())
	return err != nil
}

var upstreamAlwaysRemote atomic.Bool

// TreatUpstreamAsRemote 让此后设置的上游代理一律按不在本机处理，返回恢复原判定的函数。测试里的
// 代理只能监听在本机，要靠它覆盖透明连接经远端上游的路径。
func TreatUpstreamAsRemote() (restore func()) {
	previous := upstreamAlwaysRemote.Swap(true)
	return func() { upstreamAlwaysRemote.Store(previous) }
}

// URL 返回上游代理地址的副本，nil 表示直连。
func (p *UpstreamProxy) URL() *url.URL {
	if p == nil {
		return nil
	}
	cp := *p.url
	return &cp
}

// Local 报告上游代理是否在本机。
func (p *UpstreamProxy) Local() bool { return p != nil && p.local }

// Unresolved 报告 Local 是不是解析域名失败后的推定。
func (p *UpstreamProxy) Unresolved() bool { return p != nil && p.unresolved }

// For 返回一条连接实际要走的上游代理，nil 表示直连。transparent 为真表示连接经虚拟网卡而来：
// 上游在本机时它直连，不交给上游，以免上游自己的出站绕回 sniffy 形成回环。
func (p *UpstreamProxy) For(transparent bool) *url.URL {
	if p == nil || (transparent && p.local) {
		return nil
	}
	return p.url
}

func isLocalHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && isLocalAddr(addr)
}

func isLocalAddr(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	if addr.IsLoopback() || addr.IsUnspecified() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip, ok := netip.AddrFromSlice(ipNet.IP); ok && ip.Unmap() == addr {
			return true
		}
	}
	return false
}
