// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"encoding/binary"
	"net"
	"strings"

	"golang.org/x/sys/windows"
)

// ws2ipdef.h 里的选项号，x/sys/windows 未导出。
const (
	ipUnicastIF   = 31
	ipv6UnicastIF = 31
)

func bindToInterface(fd uintptr, network string, iface *net.Interface) error {
	h := windows.Handle(fd)
	if strings.HasSuffix(network, "6") {
		return windows.SetsockoptInt(h, windows.IPPROTO_IPV6, ipv6UnicastIF, iface.Index)
	}
	return windows.SetsockoptInt(h, windows.IPPROTO_IP, ipUnicastIF, int(ipv4UnicastIFValue(iface.Index)))
}

// IP_UNICAST_IF 的接口索引要求网络字节序，IPV6_UNICAST_IF 则是主机字节序。
func ipv4UnicastIFValue(index int) int32 {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(index))
	return int32(binary.NativeEndian.Uint32(b[:]))
}
