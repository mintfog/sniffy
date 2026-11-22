// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/mintfog/sniffy/internal/netbind"
)

// requireAdminWithWintun 在不满足条件时跳过测试：创建 wintun 网卡要管理员权限，wintun.dll 要与测试
// 程序放在同一目录（go test -c 编出测试程序，与 wintun.dll 放在一起，在提权的终端里运行）。
func requireAdminWithWintun(t *testing.T) {
	t.Helper()
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("需要管理员权限")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "wintun.dll")); err != nil {
		t.Skip("wintun.dll 须与测试程序放在同一目录")
	}
}

// useDocumentationRoutes 把网卡要装的路由换成文档保留网段：宿主上没有独立的网络命名空间，装上 /1
// 路由会接管整机流量。
func useDocumentationRoutes(t *testing.T) {
	saved4, saved6 := routes4, routes6
	routes4 = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	routes6 = []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")}
	t.Cleanup(func() { routes4, routes6 = saved4, saved6 })
}

// 运行中开始、停止接管 IPv6：地址与路由随之配上、撤掉，IPv4 不受影响。
func TestSetIPv6(t *testing.T) {
	requireAdminWithWintun(t)
	useDocumentationRoutes(t)
	accepted := make(chan string, 4)
	d, err := Start(Options{
		Handler: func(conn net.Conn) {
			accepted <- conn.LocalAddr().String()
			conn.Close()
		},
		ListenUDP: func(string) (PacketConn, error) { return nil, errors.New("未提供") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	iface, err := net.InterfaceByName(d.Name())
	if err != nil {
		t.Fatal(err)
	}

	expectIPv6 := func(on bool) {
		t.Helper()
		if got := hasAddr(t, d.Name(), addr6.Addr()); got != on {
			t.Errorf("网卡上有 IPv6 地址 = %v，期望 %v", got, on)
		}
		if n, want := countRoutes(t, iface.Index, windows.AF_INET6, routes6), map[bool]int{true: len(routes6)}[on]; n != want {
			t.Errorf("网卡上有 %d 条 IPv6 路由，期望 %d", n, want)
		}
		if got := dialsInto(accepted, "[2001:db8::7]:80"); got != on {
			t.Errorf("发往 2001:db8::7 的连接进了虚拟网卡 = %v，期望 %v", got, on)
		}
		if n := countRoutes(t, iface.Index, windows.AF_INET, routes4); n != len(routes4) {
			t.Errorf("网卡上有 %d 条 IPv4 路由，期望 %d", n, len(routes4))
		}
		if !dialsInto(accepted, "203.0.113.7:80") {
			t.Fatal("IPv4 应照旧进虚拟网卡")
		}
	}

	expectIPv6(false)
	for _, on := range []bool{true, true, false, false, true} {
		if err := d.SetIPv6(on); err != nil {
			t.Fatalf("SetIPv6(%v): %v", on, err)
		}
		expectIPv6(on)
	}
	d.Close()
	if n := countRoutes(t, iface.Index, windows.AF_INET6, routes6) + countRoutes(t, iface.Index, windows.AF_INET, routes4); n != 0 {
		t.Errorf("Close 后还剩 %d 条路由", n)
	}
	if err := d.SetIPv6(false); err != nil {
		t.Errorf("网卡关闭后 SetIPv6 应什么都不做，返回 %v", err)
	}
}

// dialsInto 拨 addr，返回连接是否进了虚拟网卡：Handler 收到了目标为 addr 的连接。
func dialsInto(accepted <-chan string, addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	select {
	case got := <-accepted:
		return got == addr
	case <-time.After(time.Second):
		return false
	}
}

// countRoutes 数出网卡 index 上目标属于 prefixes 的路由。
func countRoutes(t *testing.T, index int, family uint16, prefixes []netip.Prefix) int {
	t.Helper()
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(family, &table); err != nil {
		t.Fatal(err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	n := 0
	for _, row := range table.Rows() {
		if int(row.InterfaceIndex) == index && slices.Contains(prefixes, prefixOf(&row.DestinationPrefix)) {
			n++
		}
	}
	return n
}

// 绕过列表的路由行：目标、度量值（残留的标记）与下一跳取自默认路由。不需要管理员权限。
func TestBypassRow(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		via, err := netbind.DefaultRoute(ipv6)
		if err != nil {
			t.Fatal(err)
		}
		if via.Index == 0 {
			continue
		}
		dst := netip.MustParsePrefix("198.51.100.0/24")
		if ipv6 {
			dst = netip.MustParsePrefix("2001:db8:8000::/33")
		}
		row, err := bypassRow(dst, via)
		if err != nil {
			t.Fatal(err)
		}
		if got := prefixOf(&row.DestinationPrefix); got != dst {
			t.Errorf("目标 = %s，期望 %s", got, dst)
		}
		if row.Metric != bypassRouteMetric || int(row.InterfaceIndex) != 0 && int(row.InterfaceIndex) != via.Index {
			t.Errorf("度量值 %d、网卡 %d，期望 %d、%d", row.Metric, row.InterfaceIndex, bypassRouteMetric, via.Index)
		}
		var hop netip.Addr
		if ipv6 {
			hop = netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(&row.NextHop)).Addr)
		} else {
			hop = netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(&row.NextHop)).Addr)
		}
		if via.Gateway.IsValid() && hop != via.Gateway || !via.Gateway.IsValid() && !hop.IsUnspecified() {
			t.Errorf("下一跳 = %s，默认路由的网关为 %s", hop, via.Gateway)
		}
		// 默认路由本身就是一条目标恰为 /0 的路由。
		if scan, err := (systemRoutes{}).scan(ipv6); err != nil || !scan.taken[netip.PrefixFrom(dst.Addr(), 0).Masked()] {
			t.Errorf("scan(%v) = %v, %v，期望默认路由的网段已被占用", ipv6, scan.taken, err)
		}
	}
}

// 网卡已不在时（默认路由换到别的网卡之后它被拔掉），删它上面的绕过路由按路由不存在处理。
func TestDeleteBypassRouteGoneInterface(t *testing.T) {
	// net.Interfaces 不列出隐藏的网卡，取一个远超实际网卡数的索引。
	const gone = 0x7fff0000
	via := netbind.Route{Index: gone, Gateway: netip.MustParseAddr("192.0.2.1")}
	if err := (systemRoutes{}).delete(netip.MustParsePrefix("198.51.100.0/24"), via); !errors.Is(err, errRouteNotFound) {
		t.Fatalf("delete 经不存在的网卡 %d = %v，期望 errRouteNotFound", gone, err)
	}
}

// 绕过列表的路由经默认路由的网关装上，随 IPv6 的接管增删，RefreshBypass 后照旧在，Close 后删除；上次
// 残留的在开启时清掉，系统里已有同一网段路由的不再添加。
func TestBypassRoutes(t *testing.T) {
	requireAdminWithWintun(t)
	useDocumentationRoutes(t)
	via, err := netbind.DefaultRoute(false)
	if err != nil || via.Index == 0 {
		t.Skipf("没有 IPv4 默认路由: %v", err)
	}
	stale := netip.MustParsePrefix("203.0.113.64/26")
	if err := (systemRoutes{}).add(stale, via); err != nil {
		t.Fatalf("预置残留路由: %v", err)
	}
	t.Cleanup(func() { _ = (systemRoutes{}).delete(stale, via) })

	bypass := []netip.Prefix{netip.MustParsePrefix("203.0.113.128/25"), netip.MustParsePrefix("2001:db8:8000::/33")}
	d, err := Start(Options{IPv6: true, Bypass: bypass,
		Handler:   func(conn net.Conn) { conn.Close() },
		ListenUDP: func(string) (PacketConn, error) { return nil, errors.New("未提供") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	via6, _ := netbind.DefaultRoute(true)
	expect := func(prefix netip.Prefix, index int, want int) {
		t.Helper()
		family := familyOf(prefix.Addr())
		if n := countRoutes(t, index, family, []netip.Prefix{prefix}); n != want {
			t.Errorf("网卡 %d 上到 %s 的路由 %d 条，期望 %d", index, prefix, n, want)
		}
	}
	expect(stale, via.Index, 0)
	expect(bypass[0], via.Index, 1)
	if via6.Index != 0 {
		expect(bypass[1], via6.Index, 1)
	}
	if err := d.RefreshBypass(); err != nil {
		t.Fatalf("RefreshBypass: %v", err)
	}
	expect(bypass[0], via.Index, 1)
	if err := d.SetIPv6(false); err != nil {
		t.Fatal(err)
	}
	if via6.Index != 0 {
		expect(bypass[1], via6.Index, 0)
	}
	d.Close()
	expect(bypass[0], via.Index, 0)
}

func familyOf(addr netip.Addr) uint16 {
	if addr.Is4() {
		return windows.AF_INET
	}
	return windows.AF_INET6
}
