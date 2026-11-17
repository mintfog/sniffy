// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// defaultRouteIndex 返回默认路由所在网卡的索引，没有默认路由时返回 0。有多条时取路由度量值
// 与网卡度量值之和最小的一条，Windows 按这个和选路。
func defaultRouteIndex(ipv6 bool) (int, error) {
	family := uint16(windows.AF_INET)
	if ipv6 {
		family = windows.AF_INET6
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(family, &table); err != nil {
		return 0, err
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	best, bestMetric := 0, uint64(0)
	for _, row := range table.Rows() {
		if row.DestinationPrefix.PrefixLength != 0 || row.Loopback != 0 {
			continue
		}
		iface := windows.MibIpInterfaceRow{Family: family, InterfaceLuid: row.InterfaceLuid}
		if windows.GetIpInterfaceEntry(&iface) != nil || iface.Connected == 0 {
			continue
		}
		metric := uint64(row.Metric) + uint64(iface.Metric)
		if best == 0 || metric < bestMetric {
			best, bestMetric = int(row.InterfaceIndex), metric
		}
	}
	return best, nil
}
