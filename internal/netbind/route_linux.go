// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"encoding/binary"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

// defaultRoute 返回 main 表里的默认路由，没有时 Index 为 0。
func defaultRoute(ipv6 bool) (Route, error) {
	family := syscall.AF_INET
	if ipv6 {
		family = syscall.AF_INET6
	}
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETROUTE, family)
	if err != nil {
		return Route{}, err
	}
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return Route{}, err
	}
	return pickDefaultRoute(msgs), nil
}

// pickDefaultRoute 从路由表的转储里挑出 main 表中可用的默认路由，有多条时取度量值最小的
// 一条（内核优先用它）；没有时 Index 为 0。
func pickDefaultRoute(msgs []syscall.NetlinkMessage) Route {
	var best Route
	bestMetric := uint32(0)
	for i := range msgs {
		m := &msgs[i]
		if m.Header.Type != syscall.RTM_NEWROUTE || len(m.Data) < unix.SizeofRtMsg {
			continue
		}
		// struct rtmsg：family、dst_len、src_len、tos、table、protocol、scope、type、flags。
		dstLen, table, routeType := m.Data[1], uint32(m.Data[4]), m.Data[7]
		flags := binary.NativeEndian.Uint32(m.Data[8:])
		if dstLen != 0 || routeType != unix.RTN_UNICAST || flags&(unix.RTNH_F_DEAD|unix.RTNH_F_LINKDOWN) != 0 {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(m)
		if err != nil {
			continue
		}
		var r Route
		metric := uint32(0)
		for _, a := range attrs {
			switch a.Attr.Type {
			case unix.RTA_TABLE:
				table = binary.NativeEndian.Uint32(a.Value)
			case unix.RTA_OIF:
				r.Index = int(binary.NativeEndian.Uint32(a.Value))
			case unix.RTA_GATEWAY:
				r.Gateway, _ = netip.AddrFromSlice(a.Value)
			case unix.RTA_PRIORITY:
				metric = binary.NativeEndian.Uint32(a.Value)
			case unix.RTA_MULTIPATH:
				if r.Index == 0 {
					r = usableNexthop(a.Value)
				}
			}
		}
		if table != unix.RT_TABLE_MAIN || r.Index == 0 {
			continue
		}
		if best.Index == 0 || metric < bestMetric {
			best, bestMetric = r, metric
		}
	}
	return best
}

// usableNexthop 返回多下一跳路由里第一个可用（未标记 DEAD、LINKDOWN）的下一跳，都不可用时
// Index 为 0。b 是一串 struct rtnexthop（len、flags、hops、ifindex），各自后跟该下一跳的属性，
// 按 4 字节对齐。
func usableNexthop(b []byte) Route {
	for len(b) >= unix.SizeofRtNexthop {
		l := int(binary.NativeEndian.Uint16(b))
		if l < unix.SizeofRtNexthop || l > len(b) {
			return Route{}
		}
		if b[2]&(unix.RTNH_F_DEAD|unix.RTNH_F_LINKDOWN) == 0 {
			return Route{Index: int(binary.NativeEndian.Uint32(b[4:])), Gateway: nexthopGateway(b[unix.SizeofRtNexthop:l])}
		}
		b = b[min((l+3)&^3, len(b)):]
	}
	return Route{}
}

// nexthopGateway 从一个下一跳的属性里取出网关，没有时返回无效地址。
func nexthopGateway(b []byte) netip.Addr {
	for len(b) >= unix.SizeofRtAttr {
		l := int(binary.NativeEndian.Uint16(b))
		if l < unix.SizeofRtAttr || l > len(b) {
			break
		}
		if binary.NativeEndian.Uint16(b[2:]) == unix.RTA_GATEWAY {
			addr, _ := netip.AddrFromSlice(b[unix.SizeofRtAttr:l])
			return addr
		}
		b = b[min((l+3)&^3, len(b)):]
	}
	return netip.Addr{}
}
