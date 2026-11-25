// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// socketInodeOf 经 /proc/self/fd 读出连接的 socket inode，作为对照。
func socketInodeOf(t *testing.T, conn net.Conn) uint64 {
	t.Helper()
	raw, err := conn.(*net.TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var inode uint64
	if err := raw.Control(func(fd uintptr) {
		link, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := fmt.Sscanf(link, "socket:[%d]", &inode); err != nil {
			t.Errorf("解析 %q: %v", link, err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	return inode
}

func TestSockDiagInode(t *testing.T) {
	for _, tc := range []struct {
		name, listen, dial string
	}{
		{name: "IPv4", listen: "127.0.0.1:0", dial: "127.0.0.1"},
		{name: "IPv6", listen: "[::1]:0", dial: "::1"},
		// 双栈监听接下的 IPv4 连接是以 IPv4 映射地址工作的 IPv6 socket。
		{name: "IPv4 映射", listen: "[::]:0", dial: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", tc.listen)
			if err != nil {
				t.Skip(err)
			}
			defer listener.Close()
			port := listener.Addr().(*net.TCPAddr).Port
			client, err := net.Dial("tcp", net.JoinHostPort(tc.dial, fmt.Sprint(port)))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			server, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()

			for side, conn := range map[string]net.Conn{"客户端": client, "服务端": server} {
				local := conn.LocalAddr().(*net.TCPAddr).AddrPort()
				remote := conn.RemoteAddr().(*net.TCPAddr).AddrPort()
				local = netip.AddrPortFrom(local.Addr().Unmap(), local.Port())
				remote = netip.AddrPortFrom(remote.Addr().Unmap(), remote.Port())
				got, err := sockDiagInode(local, remote)
				if err != nil {
					t.Fatalf("%s: %v", side, err)
				}
				if want := socketInodeOf(t, conn); got != want {
					t.Fatalf("%s: inode = %d，期望 %d", side, got, want)
				}
			}
		})
	}
}

func TestSockDiagInodeNotFound(t *testing.T) {
	conn := openOwnerTestConnection(t)
	remote := conn.RemoteAddr().(*net.TCPAddr).AddrPort()
	missing := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 1)
	if _, err := sockDiagInode(missing, remote); !errors.Is(err, unix.ENOENT) {
		t.Fatalf("不存在的连接: %v，期望 ENOENT", err)
	}
}
