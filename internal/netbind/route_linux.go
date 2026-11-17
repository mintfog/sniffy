// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"encoding/binary"
	"syscall"

	"golang.org/x/sys/unix"
)

// defaultRouteIndex 返回 main 表里默认路由所在网卡的索引，没有默认路由时返回 0。
func defaultRouteIndex(ipv6 bool) (int, error) {
	family := syscall.AF_INET
	if ipv6 {
		family = syscall.AF_INET6
	}
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETROUTE, family)
	if err != nil {
		return 0, err
	}
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return 0, err
	}
	return pickDefaultRoute(msgs), nil
}

// pickDefaultRoute 从路由表的转储里挑出 main 表中可用的默认路由，有多条时取度量值最小的
// 一条（内核优先用它），返回其网卡索引；没有时返回 0。
func pickDefaultRoute(msgs []syscall.NetlinkMessage) int {
	best, bestMetric := 0, uint32(0)
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
		index, metric := 0, uint32(0)
		for _, a := range attrs {
			switch a.Attr.Type {
			case unix.RTA_TABLE:
				table = binary.NativeEndian.Uint32(a.Value)
			case unix.RTA_OIF:
				index = int(binary.NativeEndian.Uint32(a.Value))
			case unix.RTA_PRIORITY:
				metric = binary.NativeEndian.Uint32(a.Value)
			case unix.RTA_MULTIPATH:
				if index == 0 {
					index = usableNexthop(a.Value)
				}
			}
		}
		if table != unix.RT_TABLE_MAIN || index == 0 {
			continue
		}
		if best == 0 || metric < bestMetric {
			best, bestMetric = index, metric
		}
	}
	return best
}

// usableNexthop 返回多下一跳路由里第一个可用（未标记 DEAD、LINKDOWN）的下一跳的网卡索引，
// 都不可用时返回 0。b 是一串 struct rtnexthop（len、flags、hops、ifindex），各自后跟该下一跳的
// 属性，按 4 字节对齐。
func usableNexthop(b []byte) int {
	for len(b) >= unix.SizeofRtNexthop {
		l := int(binary.NativeEndian.Uint16(b))
		if l < unix.SizeofRtNexthop || l > len(b) {
			return 0
		}
		if b[2]&(unix.RTNH_F_DEAD|unix.RTNH_F_LINKDOWN) == 0 {
			return int(binary.NativeEndian.Uint32(b[4:]))
		}
		b = b[min((l+3)&^3, len(b)):]
	}
	return 0
}
