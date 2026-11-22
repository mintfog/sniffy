// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"net/netip"
	"testing"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func TestIsDefaultRoute(t *testing.T) {
	addrs := func(dst, mask route.Addr) []route.Addr {
		a := make([]route.Addr, unix.RTAX_MAX)
		a[unix.RTAX_DST], a[unix.RTAX_NETMASK] = dst, mask
		return a
	}
	zero4, zero6 := &route.Inet4Addr{}, &route.Inet6Addr{}
	cases := []struct {
		name  string
		addrs []route.Addr
		want  bool
	}{
		{"IPv4 默认路由", addrs(zero4, zero4), true},
		{"IPv6 默认路由", addrs(zero6, zero6), true},
		{"掩码缺省", addrs(zero4, nil), true},
		{"0.0.0.0/1", addrs(zero4, &route.Inet4Addr{IP: [4]byte{128}}), false},
		{"::/1", addrs(zero6, &route.Inet6Addr{IP: [16]byte{0x80}}), false},
		{"8.8.8.8", addrs(&route.Inet4Addr{IP: [4]byte{8, 8, 8, 8}}, nil), false},
		{"没有地址", nil, false},
	}
	for _, tc := range cases {
		if got := isDefaultRoute(tc.addrs); got != tc.want {
			t.Errorf("%s: isDefaultRoute = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

func TestGatewayOf(t *testing.T) {
	addrs := func(gateway route.Addr) []route.Addr {
		a := make([]route.Addr, unix.RTAX_MAX)
		a[unix.RTAX_GATEWAY] = gateway
		return a
	}
	cases := []struct {
		name  string
		addrs []route.Addr
		want  netip.Addr
	}{
		{"IPv4 网关", addrs(&route.Inet4Addr{IP: [4]byte{192, 168, 1, 1}}), netip.MustParseAddr("192.168.1.1")},
		{"IPv6 链路本地网关", addrs(&route.Inet6Addr{IP: netip.MustParseAddr("fe80::1").As16(), ZoneID: 4}), netip.MustParseAddr("fe80::1")},
		{"点对点链路", addrs(&route.LinkAddr{Index: 12}), netip.Addr{}},
		{"没有地址", nil, netip.Addr{}},
	}
	for _, tc := range cases {
		if got := gatewayOf(tc.addrs); got != tc.want {
			t.Errorf("%s: gatewayOf = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}
