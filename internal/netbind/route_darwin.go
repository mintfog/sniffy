// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"syscall"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// defaultRouteIndex 返回默认路由所在网卡的索引，没有默认路由时返回 0。
//
// macOS 给每块有网关的网卡各配一条只对绑定了该网卡的 socket 生效的默认路由（带 RTF_IFSCOPE），
// 系统实际用的是不带这个标志的那一条。
func defaultRouteIndex(ipv6 bool) (int, error) {
	family := syscall.AF_INET
	if ipv6 {
		family = syscall.AF_INET6
	}
	rib, err := route.FetchRIB(family, route.RIBTypeRoute, 0)
	if err != nil {
		return 0, err
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return 0, err
	}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || rm.Flags&unix.RTF_UP == 0 || rm.Flags&(unix.RTF_IFSCOPE|unix.RTF_HOST) != 0 {
			continue
		}
		if isDefaultRoute(rm.Addrs) {
			return rm.Index, nil
		}
	}
	return 0, nil
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
