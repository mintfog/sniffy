// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

// defaultRoute 返回默认路由，没有时 Index 为 0。有多条时取路由度量值与网卡度量值之和最小的一条，
// Windows 按这个和选路。
func defaultRoute(ipv6 bool) (Route, error) {
	family := uint16(windows.AF_INET)
	if ipv6 {
		family = windows.AF_INET6
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(family, &table); err != nil {
		return Route{}, err
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	var best Route
	bestMetric := uint64(0)
	for _, row := range table.Rows() {
		if row.DestinationPrefix.PrefixLength != 0 || row.Loopback != 0 {
			continue
		}
		iface := windows.MibIpInterfaceRow{Family: family, InterfaceLuid: row.InterfaceLuid}
		if windows.GetIpInterfaceEntry(&iface) != nil || iface.Connected == 0 {
			continue
		}
		metric := uint64(row.Metric) + uint64(iface.Metric)
		if best.Index == 0 || metric < bestMetric {
			best, bestMetric = Route{Index: int(row.InterfaceIndex), Gateway: sockaddrAddr(&row.NextHop)}, metric
		}
	}
	return best, nil
}

// sockaddrAddr 取出 SOCKADDR_INET 里的地址，不带 zone；全零地址（经网卡直接可达的路由的下一跳）
// 返回无效地址。
func sockaddrAddr(sa *windows.RawSockaddrInet) netip.Addr {
	var addr netip.Addr
	switch sa.Family {
	case windows.AF_INET:
		addr = netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(sa)).Addr)
	case windows.AF_INET6:
		addr = netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(sa)).Addr)
	}
	if addr.IsUnspecified() {
		return netip.Addr{}
	}
	return addr
}
