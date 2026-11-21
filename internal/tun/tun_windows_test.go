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

// prefixOf 把 IP Helper 的 IP_ADDRESS_PREFIX 转成 netip.Prefix，与 setSockaddr 相反。
func prefixOf(p *windows.IpAddressPrefix) netip.Prefix {
	var addr netip.Addr
	if p.Prefix.Family == windows.AF_INET {
		addr = netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(&p.Prefix)).Addr)
	} else {
		addr = netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(&p.Prefix)).Addr)
	}
	return netip.PrefixFrom(addr, int(p.PrefixLength))
}
