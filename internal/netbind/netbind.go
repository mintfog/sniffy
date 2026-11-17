// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package netbind 把出站 socket 绑定到指定网卡，使它不受路由表里指向别处的更优路由影响。
package netbind

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// Egress 描述出站怎么绑定网卡。
type Egress struct {
	// IPv4、IPv6 是两个地址族要绑定的网卡，分开指定：两者的默认路由可能在不同的网卡上。为 nil
	// 的地址族不绑定。
	IPv4, IPv6 *net.Interface

	// Tunnel 是虚拟网卡自身的地址及其网段，如 172.29.113.1/30。只有本来会被路由进虚拟网卡的
	// 连接才绑定，其余照原有路由走，经别的网卡才能到达的内网、本机地址都不受影响。为空时不绑定
	// 任何连接。
	Tunnel []netip.Prefix
}

// For 返回 network（如 "tcp4"、"udp6"）所属地址族要绑定的网卡。
func (e Egress) For(network string) *net.Interface {
	if strings.HasSuffix(network, "6") {
		return e.IPv6
	}
	return e.IPv4
}

func (e Egress) String() string {
	name := func(iface *net.Interface) string {
		if iface == nil {
			return "无"
		}
		return iface.Name
	}
	return "IPv4 " + name(e.IPv4) + "，IPv6 " + name(e.IPv6)
}

// Dialer 返回出站按 egress 绑定网卡的拨号器，连接超时为 timeout。绑定只作用于连接本身的
// socket，拨域名时的解析照系统默认方式进行。
func Dialer(egress Egress, timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: bindControl(egress)}
}

// ListenUDP 创建一个出站绑定到 iface 的 UDP socket，network 为 "udp4" 或 "udp6"。
//
// 这个 socket 不区分目标一律绑定，只适用于发往非本机地址的数据报。
func ListenUDP(iface *net.Interface, network string) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, _ string, c syscall.RawConn) error {
		var bindErr error
		if err := c.Control(func(fd uintptr) { bindErr = bindToInterface(fd, network, iface) }); err != nil {
			return err
		}
		if bindErr != nil {
			return fmt.Errorf("绑定网卡 %s: %w", iface.Name, bindErr)
		}
		return nil
	}}
	pc, err := lc.ListenPacket(context.Background(), network, ":0")
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

// DefaultEgress 返回 IPv4、IPv6 各自默认路由所在的网卡，没有默认路由的地址族为 nil。
//
// 读的是系统路由表里的默认路由（前缀长度为 0），有多条时取系统实际会用的那条。不能拿某个
// 公网地址的选路结果代替：VPN 分流、防 DNS 泄漏常给个别公网地址（如公共 DNS）配专用路由，
// 选到的就不是默认路由所在的网卡。虚拟网卡添加的 /1 路由不是默认路由，不影响结果。
func DefaultEgress() (Egress, error) {
	ipv4, err := defaultInterface(false)
	if err != nil {
		return Egress{}, err
	}
	ipv6, err := defaultInterface(true)
	if err != nil {
		return Egress{}, err
	}
	return Egress{IPv4: ipv4, IPv6: ipv6}, nil
}

func defaultInterface(ipv6 bool) (*net.Interface, error) {
	index, err := defaultRouteIndex(ipv6)
	if err != nil {
		return nil, fmt.Errorf("读取路由表: %w", err)
	}
	if index == 0 {
		return nil, nil
	}
	return net.InterfaceByIndex(index)
}

// bindControl 返回 net.Dialer 的 Control 回调。address 解析不出 IP 时照常绑定：漏绑的连接
// 可能落到别的路由上，宁可让它拨号失败。
func bindControl(egress Egress) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		iface := egress.For(network)
		if iface == nil {
			return nil
		}
		if ap, err := netip.ParseAddrPort(address); err == nil && !egress.entersTunnel(network, ap) {
			return nil
		}
		var bindErr error
		if err := c.Control(func(fd uintptr) { bindErr = bindToInterface(fd, network, iface) }); err != nil {
			return err
		}
		if bindErr != nil {
			return fmt.Errorf("绑定网卡 %s: %w", iface.Name, bindErr)
		}
		return nil
	}
}

// entersTunnel 判断发往 dst 的连接不绑定网卡时会不会进虚拟网卡：对 dst 建一个 UDP「连接」只让
// 内核选路、不发出数据报，选中的源地址属于虚拟网卡就是会。按内核的实际选路判断，VPN、容器
// 网络、策略路由给目标配的更具体的路由都照样生效；本机地址由内核在本机投递，源地址就是本机地址。
// 选不出路由时按会处理：宁可让拨号失败，也不能让连接进虚拟网卡形成回环。
func (e Egress) entersTunnel(network string, dst netip.AddrPort) bool {
	addr := dst.Addr().Unmap()
	// 回环与未指定地址必在本机投递，不必探测（各平台对 UDP 连接未指定地址的处理也不一致）；目标
	// 正是虚拟网卡自己的地址时同样是本机投递。只认精确地址：网段里的其它地址经虚拟网卡直达，
	// 漏绑会让 sniffy 代发的连接再进虚拟网卡，一层层递归转发下去。
	if addr.IsLoopback() || addr.IsUnspecified() || e.isTunnelAddr(addr) {
		return false
	}
	probe := "udp4"
	if strings.HasSuffix(network, "6") {
		probe = "udp6"
	}
	conn, err := net.DialUDP(probe, nil, net.UDPAddrFromAddrPort(dst))
	if err != nil {
		return true
	}
	src := conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr()
	conn.Close()
	return inPrefixes(src.Unmap(), e.Tunnel)
}

func (e Egress) isTunnelAddr(addr netip.Addr) bool {
	addr = addr.WithZone("")
	for _, p := range e.Tunnel {
		if p.Addr() == addr {
			return true
		}
	}
	return false
}

func inPrefixes(addr netip.Addr, prefixes []netip.Prefix) bool {
	addr = addr.WithZone("")
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
