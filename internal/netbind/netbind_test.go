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
				return &iface, localKey(ip.WithZone(iface.Name))
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

func TestBindControl(t *testing.T) {
	iface, own := physicalInterface(t)
	local, err := localAddrs()
	if err != nil {
		t.Fatal(err)
	}
	control := bindControl(iface, local)
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

func TestIsLocal(t *testing.T) {
	local := map[netip.Addr]struct{}{
		netip.MustParseAddr("192.168.1.12"):   {},
		netip.MustParseAddr("fe80::1%eth0"):   {},
		netip.MustParseAddr("2001:db8::12"):   {},
		netip.MustParseAddr("10.255.255.254"): {},
	}
	cases := []struct {
		addr string
		want bool
	}{
		{"192.168.1.12", true},
		{"10.255.255.254", true},
		{"::ffff:192.168.1.12", true},
		{"::ffff:127.0.0.1", true},
		{"::1%lo", true},
		{"fe80::1%eth0", true},
		{"2001:db8::12", true},
		{"2001:db8::12%eth1", true},
		{"192.168.1.13", false},
		{"fe80::1%eth1", false},
		{"fe80::1", false},
		{"fe80::2%eth0", false},
		{"203.0.113.1", false},
	}
	for _, tc := range cases {
		if got := isLocal(netip.MustParseAddr(tc.addr), local); got != tc.want {
			t.Errorf("isLocal(%s) = %v，期望 %v", tc.addr, got, tc.want)
		}
	}
}

func TestAddLocalZoneForms(t *testing.T) {
	local := make(map[netip.Addr]struct{})
	iface := net.Interface{Index: 2, Name: "eth0"}
	addLocal(local, iface, []net.Addr{
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("192.168.1.12"), Mask: net.CIDRMask(24, 32)},
	})
	cases := []struct {
		addr string
		want bool
	}{
		{"fe80::1%eth0", true},
		{"fe80::1%2", true},
		{"fe80::1%3", false},
		{"fe80::1", false},
		{"192.168.1.12", true},
	}
	for _, tc := range cases {
		if got := isLocal(netip.MustParseAddr(tc.addr), local); got != tc.want {
			t.Errorf("isLocal(%s) = %v，期望 %v", tc.addr, got, tc.want)
		}
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

	dialer, err := Dialer(iface, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		conn, err := dialer.Dial("tcp4", net.JoinHostPort(host, port))
		if err != nil {
			t.Errorf("绑定 %s 后拨 %s: %v", iface.Name, host, err)
			continue
		}
		_ = conn.Close()
	}
}

// BenchmarkDialExternal 每轮新建一个指向外部地址的 UDP socket（不发包），对比绑定网卡的开销。
func BenchmarkDialExternal(b *testing.B) {
	iface, _ := physicalInterface(b)
	bound, err := Dialer(iface, time.Second)
	if err != nil {
		b.Fatal(err)
	}
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
