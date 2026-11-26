// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"net"
	"strings"

	"golang.org/x/sys/unix"
)

func isBound(fd uintptr, network string, iface *net.Interface) (bool, error) {
	level, opt := unix.IPPROTO_IP, unix.IP_BOUND_IF
	if strings.HasSuffix(network, "6") {
		level, opt = unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF
	}
	index, err := unix.GetsockoptInt(int(fd), level, opt)
	return index == iface.Index, err
}
