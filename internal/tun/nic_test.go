// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"net"
	"net/netip"
	"testing"
)

// hasAddr 判断网卡 name 上是否配着地址 want。
func hasAddr(t *testing.T, name string, want netip.Addr) bool {
	t.Helper()
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if addr, ok := netip.AddrFromSlice(ipnet.IP); ok && addr.Unmap() == want {
				return true
			}
		}
	}
	return false
}

// useDocumentationRoutes 把网卡要装的路由换成文档保留网段：宿主上没有独立的网络命名空间，装上 /1
// 路由会接管整机流量。
func useDocumentationRoutes(t *testing.T) {
	saved4, saved6 := routes4, routes6
	routes4 = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	routes6 = []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")}
	t.Cleanup(func() { routes4, routes6 = saved4, saved6 })
}
