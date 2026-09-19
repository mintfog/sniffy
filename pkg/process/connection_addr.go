// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

func connectionAddrPort(addr net.Addr) (netip.AddrPort, error) {
	var endpoint netip.AddrPort
	if tcp, ok := addr.(*net.TCPAddr); ok {
		if tcp == nil || tcp.Port <= 0 || tcp.Port > 65535 {
			return endpoint, fmt.Errorf("无效的 TCP 地址")
		}
		endpoint = tcp.AddrPort()
	} else if addr != nil {
		var err error
		endpoint, err = netip.ParseAddrPort(addr.String())
		if err != nil {
			return endpoint, fmt.Errorf("解析连接地址: %w", err)
		}
	}
	if !endpoint.IsValid() || endpoint.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("无效的连接地址")
	}
	ip := endpoint.Addr().Unmap()
	// Windows 连接表的 IPv6 scope 使用接口索引，Go 的 zone 也可能是接口名。
	if zone := ip.Zone(); zone != "" {
		index, err := strconv.ParseUint(zone, 10, 32)
		if err != nil {
			iface, err := net.InterfaceByName(zone)
			if err != nil {
				return netip.AddrPort{}, fmt.Errorf("解析 IPv6 scope %q: %w", zone, err)
			}
			index = uint64(iface.Index)
		}
		ip = ip.WithZone("")
		if index != 0 {
			ip = ip.WithZone(strconv.FormatUint(index, 10))
		}
	}
	return netip.AddrPortFrom(ip, endpoint.Port()), nil
}
