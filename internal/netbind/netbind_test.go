// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"testing"
	"time"
)

// physicalInterface 返回一块已启用、配有地址的非回环网卡及其首个地址；没有则跳过测试。
func physicalInterface(tb testing.TB) (*net.Interface, netip.Addr) {
	tb.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		tb.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip, ok := netip.AddrFromSlice(ipnet.IP); ok {
				ip = ip.Unmap()
				if ip.Is6() && ip.IsLinkLocalUnicast() {
					ip = ip.WithZone(iface.Name)
				}
				return &iface, ip
			}
		}
	}
	tb.Skip("没有已启用且配有地址的非回环网卡")
	return nil, netip.Addr{}
}

// newSocket 建一个与 network 同地址族的 UDP socket；绑定选项对同族的 TCP、UDP socket 通用。
func newSocket(t *testing.T, network string) syscall.RawConn {
	t.Helper()
	family := "udp4"
	if strings.HasSuffix(network, "6") {
		family = "udp6"
	}
	pc, err := net.ListenPacket(family, ":0")
	if err != nil {
		t.Skipf("建 %s socket: %v", family, err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	raw, err := pc.(*net.UDPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fakeTunnel 把本机发往外部地址时内核选中的源地址当作虚拟网卡的地址，按虚拟网卡的配置带上
// /30、/126 的网段返回：外部目标因此都算「本来会进虚拟网卡」而要绑定，本机目标不算。
func fakeTunnel(tb testing.TB) []netip.Prefix {
	tb.Helper()
	var prefixes []netip.Prefix
	for _, probe := range []struct{ network, addr string }{{"udp4", "192.0.2.1:9"}, {"udp6", "[2001:db8::1]:9"}} {
		conn, err := net.Dial(probe.network, probe.addr)
		if err != nil {
			continue
		}
		src := conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
		conn.Close()
		prefixes = append(prefixes, netip.PrefixFrom(src, src.BitLen()-2))
	}
	if len(prefixes) == 0 {
		tb.Skip("没有到外部地址的路由")
	}
	return prefixes
}

func TestBindControl(t *testing.T) {
	iface, own := physicalInterface(t)
	tunnel := fakeTunnel(t)
	control := bindControl(Egress{IPv4: iface, IPv6: iface, Tunnel: tunnel})
	// 虚拟网卡网段内、不是它自己的地址：内核经虚拟网卡直达，要绑定。
	sibling := tunnel[0].Addr().Next()
	if !tunnel[0].Contains(sibling) {
		sibling = tunnel[0].Addr().Prev()
	}
	tunnelNetwork := "tcp4"
	if sibling.Is6() {
		tunnelNetwork = "tcp6"
	}
	ownNetwork := "tcp4"
	if own.Is6() {
		ownNetwork = "tcp6"
	}

	cases := []struct {
		name      string
		network   string
		address   string
		wantBound bool
	}{
		{"外部 IPv4", "tcp4", "192.0.2.1:443", true},
		{"外部 IPv6", "tcp6", "[2001:db8::1]:443", true},
		{"外部 UDP", "udp4", "192.0.2.53:53", true},
		{"地址不是 IP 字面量", "tcp4", "example.com:443", true},
		{"IPv4 回环", "tcp4", "127.0.0.1:7890", false},
		{"127/8 内的其他地址", "tcp4", "127.0.0.53:53", false},
		{"IPv6 回环", "tcp6", "[::1]:7890", false},
		{"IPv4 未指定地址", "tcp4", "0.0.0.0:80", false},
		{"IPv6 未指定地址", "tcp6", "[::]:80", false},
		{"本机网卡地址", ownNetwork, netip.AddrPortFrom(own, 80).String(), false},
		{"虚拟网卡自己的地址", tunnelNetwork, netip.AddrPortFrom(tunnel[0].Addr(), 80).String(), false},
		{"虚拟网卡网段内的其它地址", tunnelNetwork, netip.AddrPortFrom(sibling, 80).String(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := newSocket(t, tc.network)
			if err := control(tc.network, tc.address, raw); err != nil {
				if errors.Is(err, syscall.EPERM) {
					t.Skipf("当前权限不能绑定网卡: %v", err)
				}
				t.Fatalf("control: %v", err)
			}
			var bound bool
			var readErr error
			if err := raw.Control(func(fd uintptr) { bound, readErr = isBound(fd, tc.network, iface) }); err != nil {
				t.Fatal(err)
			}
			if readErr != nil {
				t.Fatalf("读回绑定状态: %v", readErr)
			}
			if bound != tc.wantBound {
				t.Fatalf("绑定到 %s = %v，期望 %v", iface.Name, bound, tc.wantBound)
			}
		})
	}
}

// 虚拟网卡的地址为空时不绑定任何连接。
func TestBindControlWithoutTunnel(t *testing.T) {
	iface, _ := physicalInterface(t)
	control := bindControl(Egress{IPv4: iface, IPv6: iface})
	raw := newSocket(t, "tcp4")
	if err := control("tcp4", "192.0.2.1:443", raw); err != nil {
		t.Fatalf("control: %v", err)
	}
	var bound bool
	var readErr error
	if err := raw.Control(func(fd uintptr) { bound, readErr = isBound(fd, "tcp4", iface) }); err != nil {
		t.Fatal(err)
	}
	if readErr != nil || bound {
		t.Errorf("绑定到 %s = %v（%v），期望不绑定", iface.Name, bound, readErr)
	}
}

// TestDialerReachesLocalService：绑定物理网卡后，本机回环上的服务（如本机上游代理）按 IP、
// 按主机名都能拨通。
func TestDialerReachesLocalService(t *testing.T) {
	iface, _ := physicalInterface(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	dialer := Dialer(Egress{IPv4: iface, IPv6: iface, Tunnel: fakeTunnel(t)}, 2*time.Second)
	for _, host := range []string{"127.0.0.1", "localhost"} {
		conn, err := dialer.Dial("tcp4", net.JoinHostPort(host, port))
		if err != nil {
			t.Errorf("绑定 %s 后拨 %s: %v", iface.Name, host, err)
			continue
		}
		_ = conn.Close()
	}
}

// BenchmarkDialExternal 每轮新建一个指向外部地址的 UDP socket（不发包），对比绑定网卡的开销
// （含判断目标是否会进虚拟网卡的选路探测）。
func BenchmarkDialExternal(b *testing.B) {
	iface, _ := physicalInterface(b)
	bound := Dialer(Egress{IPv4: iface, IPv6: iface, Tunnel: fakeTunnel(b)}, time.Second)
	for _, bc := range []struct {
		name   string
		dialer *net.Dialer
	}{
		{"未绑定", &net.Dialer{Timeout: time.Second}},
		{"绑定", bound},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				conn, err := bc.dialer.Dial("udp", "192.0.2.1:9")
				if err != nil {
					b.Skipf("拨外部地址: %v", err)
				}
				_ = conn.Close()
			}
		})
	}
}

func TestListenUDPBinds(t *testing.T) {
	iface, _ := physicalInterface(t)
	for _, network := range []string{"udp4", "udp6"} {
		t.Run(network, func(t *testing.T) {
			conn, err := ListenUDP(iface, network)
			if err != nil {
				if errors.Is(err, syscall.EPERM) {
					t.Skipf("当前权限不能绑定网卡: %v", err)
				}
				if network == "udp6" && errors.Is(err, syscall.EAFNOSUPPORT) {
					t.Skipf("系统未启用 IPv6: %v", err)
				}
				t.Fatal(err)
			}
			defer conn.Close()
			raw, err := conn.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var bound bool
			var readErr error
			if err := raw.Control(func(fd uintptr) { bound, readErr = isBound(fd, network, iface) }); err != nil {
				t.Fatal(err)
			}
			if readErr != nil {
				t.Fatalf("读回绑定状态: %v", readErr)
			}
			if !bound {
				t.Fatalf("socket 未绑定到 %s", iface.Name)
			}
		})
	}
}

// TestBindControlPerFamily：两个地址族各绑各的网卡，没有指定网卡的地址族不绑定。
func TestBindControlPerFamily(t *testing.T) {
	iface, _ := physicalInterface(t)
	control := bindControl(Egress{IPv4: iface, Tunnel: fakeTunnel(t)})
	for _, tc := range []struct {
		network, address string
		wantBound        bool
	}{
		{"tcp4", "192.0.2.1:443", true},
		{"udp4", "192.0.2.53:53", true},
		{"tcp6", "[2001:db8::1]:443", false},
		{"udp6", "[2001:db8::53]:53", false},
	} {
		raw := newSocket(t, tc.network)
		if err := control(tc.network, tc.address, raw); err != nil {
			if errors.Is(err, syscall.EPERM) {
				t.Skipf("当前权限不能绑定网卡: %v", err)
			}
			t.Fatalf("%s control: %v", tc.network, err)
		}
		var bound bool
		var readErr error
		if err := raw.Control(func(fd uintptr) { bound, readErr = isBound(fd, tc.network, iface) }); err != nil {
			t.Fatal(err)
		}
		if readErr != nil {
			t.Fatalf("%s 读回绑定状态: %v", tc.network, readErr)
		}
		if bound != tc.wantBound {
			t.Errorf("%s 绑定到 %s = %v，期望 %v", tc.network, iface.Name, bound, tc.wantBound)
		}
	}
}

func TestEgressFor(t *testing.T) {
	v4, v6 := &net.Interface{Name: "eth0"}, &net.Interface{Name: "wlan0"}
	e := Egress{IPv4: v4, IPv6: v6}
	for network, want := range map[string]*net.Interface{"tcp4": v4, "udp4": v4, "tcp6": v6, "udp6": v6} {
		if got := e.For(network); got != want {
			t.Errorf("For(%s) = %v，期望 %s", network, got, want.Name)
		}
	}
}

func TestDefaultEgress(t *testing.T) {
	egress, err := DefaultEgress()
	if err != nil {
		t.Fatal(err)
	}
	if egress.IPv4 == nil && egress.IPv6 == nil {
		t.Skip("没有默认路由")
	}
	for _, iface := range []*net.Interface{egress.IPv4, egress.IPv6} {
		if iface != nil && (iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0) {
			t.Errorf("默认路由所在的网卡 %s 未启用或是回环网卡（flags %v）", iface.Name, iface.Flags)
		}
	}
}
