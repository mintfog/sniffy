// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"net/netip"
	"slices"
	"testing"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func TestRouteDst(t *testing.T) {
	for _, tc := range []struct {
		dst  string
		want []string
	}{
		{"10.0.0.0/8", []string{"-inet", "-net", "10.0.0.0/8"}},
		{"198.51.100.7/32", []string{"-inet", "-host", "198.51.100.7"}},
		{"2001:db8::/32", []string{"-inet6", "-net", "2001:db8::/32"}},
		{"2001:db8::1/128", []string{"-inet6", "-host", "2001:db8::1"}},
	} {
		if got := routeDst(netip.MustParsePrefix(tc.dst)); !slices.Equal(got, tc.want) {
			t.Errorf("routeDst(%s) = %q，期望 %q", tc.dst, got, tc.want)
		}
	}
}

// 带着两个标记的是绕过路由，别的不限定网卡的路由占用网段；限定网卡的与克隆出的主机路由不算。按掩码
// 或 RTF_HOST 还原出网段。
func TestClassifyRoute(t *testing.T) {
	msg := func(flags int, dst, mask route.Addr) *route.RouteMessage {
		addrs := make([]route.Addr, unix.RTAX_MAX)
		addrs[unix.RTAX_DST], addrs[unix.RTAX_NETMASK] = dst, mask
		return &route.RouteMessage{Flags: flags, Addrs: addrs}
	}
	static := unix.RTF_UP | unix.RTF_GATEWAY | unix.RTF_STATIC
	marked := static | bypassRouteMarks
	net10 := &route.Inet4Addr{IP: [4]byte{10}}
	mask8 := &route.Inet4Addr{IP: [4]byte{255}}
	host := &route.Inet4Addr{IP: [4]byte{198, 51, 100, 7}}
	for _, tc := range []struct {
		name  string
		m     *route.RouteMessage
		want  string
		owner routeOwner
	}{
		{"IPv4 网段", msg(marked, net10, mask8), "10.0.0.0/8", routeOurs},
		{"IPv4 主机", msg(marked|unix.RTF_HOST, host, nil), "198.51.100.7/32", routeOurs},
		{"IPv6 网段", msg(marked, &route.Inet6Addr{IP: netip.MustParseAddr("2001:db8::").As16()},
			&route.Inet6Addr{IP: netip.MustParseAddr("ffff:ffff::").As16()}), "2001:db8::/32", routeOurs},
		{"只有一个标记", msg(marked&^unix.RTF_PROTO2, net10, mask8), "10.0.0.0/8", routeOthers},
		{"没有标记", msg(static, net10, mask8), "10.0.0.0/8", routeOthers},
		{"限定网卡的", msg(marked|unix.RTF_IFSCOPE, net10, mask8), "", routeIgnored},
		{"克隆出的主机路由", msg(static|unix.RTF_HOST|unix.RTF_WASCLONED, host, nil), "", routeIgnored},
	} {
		got, owner := classifyRoute(tc.m)
		if owner != tc.owner || tc.owner != routeIgnored && got != netip.MustParsePrefix(tc.want) {
			t.Errorf("%s: classifyRoute = %s, %d，期望 %s, %d", tc.name, got, owner, tc.want, tc.owner)
		}
	}
}
