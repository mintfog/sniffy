// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/mintfog/sniffy/internal/netbind"
)

var (
	// errRouteExists 表示系统里已有同一网段的路由：它比指向虚拟网卡的 /1 路由更具体，发往这个网段
	// 的流量本来就不进虚拟网卡，不必再加。
	errRouteExists   = errors.New("已有同一网段的路由")
	errRouteNotFound = errors.New("路由不存在")
)

// routeScan 是读一次系统路由表得到的、某个地址族里与绕过列表有关的目标网段。
type routeScan struct {
	// own 是带着本程序标记的绕过路由。
	own map[netip.Prefix]bool
	// taken 是已有别的路由（VPN 等装的）的网段，见 errRouteExists。
	taken map[netip.Prefix]bool
}

// bypassSystem 是 bypassRouter 对系统路由表的操作，由 macOS / Windows 实现。
type bypassSystem interface {
	defaultRoute(ipv6 bool) (netbind.Route, error)
	scan(ipv6 bool) (routeScan, error)
	// add 添加一条发往 dst、经默认路由 via 的带标记的路由，已有同一网段的路由时返回 errRouteExists。
	add(dst netip.Prefix, via netbind.Route) error
	// delete 删除 add 添加的路由，不在了返回 errRouteNotFound。
	delete(dst netip.Prefix, via netbind.Route) error
}

// bypassRouter 给绕过列表的网段加经默认路由网关的更具体的路由，使发往它们的流量照常走物理网卡。
// 这些路由不随虚拟网卡消失：关闭时删除；进程被强杀后留下的指向的仍是当时的网关，与不开 TUN 时
// 一致，下次开启时按各平台的标记清掉。方法由调用方串行调用。
type bypassRouter struct {
	sys bypassSystem
	// 以下各项按地址族存放，[0] 为 IPv4、[1] 为 IPv6。
	prefixes [2][]netip.Prefix
	// active 表示该地址族正被虚拟网卡接管，要维护它的绕过路由。
	active [2]bool
	// via 是装着的路由所经的默认路由，没装时为零值。装着哪些不另记，每次按标记从路由表里读。
	via [2]netbind.Route
}

func newBypassRouter(sys bypassSystem, prefixes []netip.Prefix) *bypassRouter {
	b := &bypassRouter{sys: sys}
	for _, p := range prefixes {
		f := familyIndex(p.Addr().Is6())
		b.prefixes[f] = append(b.prefixes[f], p)
	}
	return b
}

func familyIndex(ipv6 bool) int {
	if ipv6 {
		return 1
	}
	return 0
}

// install 开始维护 IPv6（ipv6 为 true）或 IPv4 的绕过路由，按当前的默认路由装上。该地址族没有
// 默认路由时先不装，refresh 时补上。
func (b *bypassRouter) install(ipv6 bool) error {
	f := familyIndex(ipv6)
	b.active[f] = true
	if len(b.prefixes[f]) == 0 {
		return nil
	}
	via, err := b.sys.defaultRoute(ipv6)
	if err != nil {
		return err
	}
	return b.sync(f, via)
}

// remove 删除该地址族的绕过路由并停止维护。
func (b *bypassRouter) remove(ipv6 bool) error {
	f := familyIndex(ipv6)
	b.active[f] = false
	return b.del(f)
}

// refresh 让正在维护的绕过路由跟上当前的默认路由：默认路由的网卡或网关变了的地址族先删后加；
// 没变的只补上缺的（如随网卡断开被系统删掉的），装着的不动，免得删与加之间发往绕过网段的连接
// 进虚拟网卡。某个地址族没有默认路由时保留原有的：删掉的话发往绕过网段的流量会进虚拟网卡被抓
// 下来，保留着只是发不出去，与这时没有网络一致。
func (b *bypassRouter) refresh() error {
	var errs []error
	for f, ipv6 := range []bool{false, true} {
		if !b.active[f] || len(b.prefixes[f]) == 0 {
			continue
		}
		via, err := b.sys.defaultRoute(ipv6)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if via.Index == 0 {
			continue
		}
		// 同一网段只能有一条不限定网卡的路由（macOS），换网关只能先删后加。
		if via != b.via[f] {
			errs = append(errs, b.del(f))
		}
		errs = append(errs, b.sync(f, via))
	}
	return errors.Join(errs...)
}

// close 删除两个地址族的绕过路由。
func (b *bypassRouter) close() error {
	return errors.Join(b.remove(false), b.remove(true))
}

// sync 经 via 装上该地址族缺的绕过路由，系统里已有同一网段路由的跳过。via 须与装着的路由所经的
// 相同，或者已经一条都没装着。
func (b *bypassRouter) sync(f int, via netbind.Route) error {
	if via.Index == 0 {
		return nil
	}
	scan, err := b.sys.scan(f == 1)
	if err != nil {
		return err
	}
	b.via[f] = via
	var errs []error
	for _, p := range b.prefixes[f] {
		if scan.own[p] || scan.taken[p] {
			continue
		}
		if err := b.sys.add(p, via); err != nil && !errors.Is(err, errRouteExists) {
			errs = append(errs, fmt.Errorf("添加路由 %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// del 删除该地址族装着的绕过路由。已不带标记的（随网卡删掉了，或被 VPN 等改写成了自己的）不删；
// 删不掉的留到下次开启时按标记清掉。
func (b *bypassRouter) del(f int) error {
	via := b.via[f]
	b.via[f] = netbind.Route{}
	if via.Index == 0 {
		return nil
	}
	scan, err := b.sys.scan(f == 1)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range b.prefixes[f] {
		if !scan.own[p] {
			continue
		}
		if err := b.sys.delete(p, via); err != nil && !errors.Is(err, errRouteNotFound) {
			errs = append(errs, fmt.Errorf("删除路由 %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}
