// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"net"
	"strings"

	"golang.org/x/sys/windows"
)

// 读回的接口索引两个地址族都是主机字节序，IPv4 与设置时的字节序不同。
func isBound(fd uintptr, network string, iface *net.Interface) (bool, error) {
	level, opt := windows.IPPROTO_IP, ipUnicastIF
	if strings.HasSuffix(network, "6") {
		level, opt = windows.IPPROTO_IPV6, ipv6UnicastIF
	}
	index, err := windows.GetsockoptInt(windows.Handle(fd), level, opt)
	return index == iface.Index, err
}
