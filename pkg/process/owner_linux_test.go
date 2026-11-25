// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"net"
	"testing"
)

// TestProcfsInode 覆盖 sock_diag 出错时改读 /proc/net/tcp 的那条路：两者取到的 inode 一致。
func TestProcfsInode(t *testing.T) {
	requireProcTCP(t)
	f, err := newOwnerFinder()
	if err != nil {
		t.Fatal(err)
	}
	conn := openOwnerTestConnection(t)
	local, remote := conn.LocalAddr().(*net.TCPAddr).AddrPort(), conn.RemoteAddr().(*net.TCPAddr).AddrPort()
	got, err := f.procfsInode(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if want := socketInodeOf(t, conn); got != want {
		t.Fatalf("inode = %d，期望 %d", got, want)
	}
}
