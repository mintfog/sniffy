// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

// peerPID 取对端的 PID。LOCAL_PEERPID 只在连接还在时取得到，对端已经关闭的返回 errPeerGone。
func peerPID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var optErr error
	if err := raw.Control(func(fd uintptr) {
		pid, optErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return 0, err
	}
	if errors.Is(optErr, unix.ENOTCONN) {
		return 0, errPeerGone
	}
	return pid, optErr
}
