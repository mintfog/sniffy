// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sioAFUnixGetPeerPID 即 afunix.h 的 SIO_AF_UNIX_GETPEERPID（_WSAIOR(IOC_VENDOR, 256)）。
const sioAFUnixGetPeerPID = 0x58000100

func peerPID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid uint32
	var ioErr error
	if err := raw.Control(func(fd uintptr) {
		var n uint32
		ioErr = windows.WSAIoctl(windows.Handle(fd), sioAFUnixGetPeerPID, nil, 0,
			(*byte)(unsafe.Pointer(&pid)), uint32(unsafe.Sizeof(pid)), &n, nil, 0)
	}); err != nil {
		return 0, err
	}
	return int(pid), ioErr
}
