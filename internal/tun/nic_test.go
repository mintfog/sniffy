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
