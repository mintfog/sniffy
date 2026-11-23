// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"net"
	"os"
	"testing"
)

// 连接还在时取得到对端的 PID；对端关闭之后取不到，返回 errPeerGone。
func TestPeerPIDAfterPeerCloses(t *testing.T) {
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath(t, "ch"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	for _, closeFirst := range []bool{false, true} {
		dialed, err := net.DialUnix("unix", nil, ln.Addr().(*net.UnixAddr))
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := ln.AcceptUnix()
		if err != nil {
			t.Fatal(err)
		}
		if closeFirst {
			_ = dialed.Close()
		}
		pid, err := peerPID(accepted)
		switch {
		case closeFirst && !errors.Is(err, errPeerGone):
			t.Errorf("对端关闭后 peerPID = %d, %v，期望 errPeerGone", pid, err)
		case !closeFirst && (err != nil || pid != os.Getpid()):
			t.Errorf("连接还在时 peerPID = %d, %v，期望 %d", pid, err, os.Getpid())
		}
		_ = accepted.Close()
		_ = dialed.Close()
	}
}
