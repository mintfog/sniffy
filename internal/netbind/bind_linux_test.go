// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"net"

	"golang.org/x/sys/unix"
)

func isBound(fd uintptr, _ string, iface *net.Interface) (bool, error) {
	name, err := unix.GetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE)
	return name == iface.Name, err
}
