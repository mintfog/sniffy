// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"net"

	"golang.org/x/sys/unix"
)

// 非特权进程设置 SO_BINDTODEVICE 需要内核 5.7 及以上，更老的内核返回 EPERM。
func bindToInterface(fd uintptr, _ string, iface *net.Interface) error {
	return unix.BindToDevice(int(fd), iface.Name)
}
