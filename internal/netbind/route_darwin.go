// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"net/netip"
	"syscall"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// defaultRoute 返回默认路由，没有时 Index 为 0。
//
// macOS 给每块有网关的网卡各配一条只对绑定了该网卡的 socket 生效的默认路由（带 RTF_IFSCOPE），
// 系统实际用的是不带这个标志的那一条。
func defaultRoute(ipv6 bool) (Route, error) {
	family := syscall.AF_INET
	if ipv6 {
		family = syscall.AF_INET6
	}
	rib, err := route.FetchRIB(family, route.RIBTypeRoute, 0)
	if err != nil {
		return Route{}, err
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return Route{}, err
	}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || rm.Flags&unix.RTF_UP == 0 || rm.Flags&(unix.RTF_IFSCOPE|unix.RTF_HOST) != 0 {
			continue
		}
		if isDefaultRoute(rm.Addrs) {
			return Route{Index: rm.Index, Gateway: gatewayOf(rm.Addrs)}, nil
		}
	}
	return Route{}, nil
}

// gatewayOf 取出路由的网关地址。经网卡直接可达的路由（如点对点链路）网关是链路层地址，返回无效地址。
func gatewayOf(addrs []route.Addr) netip.Addr {
	if len(addrs) <= unix.RTAX_GATEWAY {
		return netip.Addr{}
	}
	switch a := addrs[unix.RTAX_GATEWAY].(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP)
	case *route.Inet6Addr:
		return netip.AddrFrom16(a.IP)
	}
	return netip.Addr{}
}

// isDefaultRoute 判断路由的目标与掩码是否都是全零。
func isDefaultRoute(addrs []route.Addr) bool {
	if len(addrs) <= unix.RTAX_NETMASK || !isZeroAddr(addrs[unix.RTAX_DST]) {
		return false
	}
	mask := addrs[unix.RTAX_NETMASK]
	return mask == nil || isZeroAddr(mask)
}

func isZeroAddr(a route.Addr) bool {
	switch a := a.(type) {
	case *route.Inet4Addr:
		return a.IP == [4]byte{}
	case *route.Inet6Addr:
		return a.IP == [16]byte{}
	}
	return false
}
