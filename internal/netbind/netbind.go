// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package netbind 把出站 socket 绑定到指定网卡，使它不受路由表里指向别处的更优路由影响。
package netbind

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"
)

// Dialer 返回出站绑定到 iface 的拨号器，连接超时为 timeout。绑定只作用于连接本身的 socket，
// 拨域名时的解析照系统默认方式进行。
//
// 目标是本机地址（回环、未指定地址、任一网卡上配置的地址）时不绑定：这类连接由内核在本机
// 投递，绑到网卡上反而拨不通。本机地址在调用时取一次快照，网卡地址变化后须重新调用。
func Dialer(iface *net.Interface, timeout time.Duration) (*net.Dialer, error) {
	local, err := localAddrs()
	if err != nil {
		return nil, fmt.Errorf("枚举本机地址: %w", err)
	}
	return &net.Dialer{Timeout: timeout, Control: bindControl(iface, local)}, nil
}

// localAddrs 返回各网卡上配置的地址，以 localKey 归一化。
func localAddrs() (map[netip.Addr]struct{}, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	local := make(map[netip.Addr]struct{})
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("网卡 %s: %w", iface.Name, err)
		}
		addLocal(local, iface, addrs)
	}
	return local, nil
}

// addLocal 把 iface 上的 addrs 加入 local。拨号时目标地址的 zone 原样传进 Control，网卡名与
// 接口索引两种写法都可能出现，链路本地地址因此按两种 zone 各存一份。
func addLocal(local map[netip.Addr]struct{}, iface net.Interface, addrs []net.Addr) {
	index := strconv.Itoa(iface.Index)
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip, ok := netip.AddrFromSlice(ipnet.IP); ok {
			local[localKey(ip.WithZone(iface.Name))] = struct{}{}
			local[localKey(ip.WithZone(index))] = struct{}{}
		}
	}
}

// localKey 把 addr 归一化成本机地址集合的键。zone 只在 IPv6 链路本地地址上保留：同一个
// fe80:: 地址可以出现在不同链路上，要连同网卡一起比较；其余地址全局唯一，zone 不参与比较。
func localKey(addr netip.Addr) netip.Addr {
	addr = addr.Unmap()
	if addr.Is6() && addr.IsLinkLocalUnicast() {
		return addr
	}
	return addr.WithZone("")
}

// bindControl 返回 net.Dialer 的 Control 回调。address 解析不出 IP 时照常绑定：漏绑的连接
// 可能落到别的路由上，宁可让它拨号失败。
func bindControl(iface *net.Interface, local map[netip.Addr]struct{}) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		if ap, err := netip.ParseAddrPort(address); err == nil && isLocal(ap.Addr(), local) {
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

// isLocal 判断 addr 是否本机地址。不带 zone 的 IPv6 链路本地地址无从确定所在链路，按非本机处理。
func isLocal(addr netip.Addr, local map[netip.Addr]struct{}) bool {
	addr = localKey(addr)
	if addr.IsLoopback() || addr.IsUnspecified() {
		return true
	}
	_, ok := local[addr]
	return ok
}
