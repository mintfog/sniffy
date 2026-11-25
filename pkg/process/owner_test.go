// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestOwnerClientProcess 是 TestFindOwnerChildProcess 拉起的子进程：连上指定地址后保持连接，直到
// 标准输入关闭。
func TestOwnerClientProcess(t *testing.T) {
	address := os.Getenv("SNIFFY_OWNER_TEST_ADDRESS")
	if address == "" {
		return
	}
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// startOwnerClient 以 name 为文件名拉起测试二进制的副本，让它连上 listener，返回副本路径、子进程与
// 服务端接下的连接。
func startOwnerClient(t testing.TB, listener net.Listener, name string) (string, *exec.Cmd, net.Conn) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	child := exec.CommandContext(ctx, path, "-test.run=^TestOwnerClientProcess$")
	child.Env = append(os.Environ(), "SNIFFY_OWNER_TEST_ADDRESS="+listener.Addr().String())
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = child.Wait()
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("子进程没有连上: %q, %v", line, err)
	}
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved, child, server
}

func TestFindOwnerChildProcess(t *testing.T) {
	finder, err := NewOwnerFinder()
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	self, _ = filepath.EvalSymlinks(self)

	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			address := "127.0.0.1:0"
			if network == "tcp6" {
				address = "[::1]:0"
			}
			listener, err := net.Listen(network, address)
			if err != nil {
				t.Skip(err)
			}
			defer listener.Close()
			childPath, child, server := startOwnerClient(t, listener, "sniffy-owner-client")
			// 应用一侧 socket 的本端是服务端看到的对端。
			local, remote := server.RemoteAddr(), server.LocalAddr()

			for _, tc := range []struct {
				name    string
				entries []string
				owned   bool
			}{
				{name: "按程序名", entries: []string{"sniffy-owner-client"}, owned: true},
				{name: "按路径", entries: []string{childPath}, owned: true},
				{name: "按所在目录", entries: []string{filepath.Dir(childPath)}, owned: true},
				{name: "名单里没有它", entries: []string{"no-such-app"}},
				// 本进程在名单里、却不持有这条连接。
				{name: "名单里的进程不持有连接", entries: []string{self}},
			} {
				pid, ok, err := finder.FindOwner(local, remote, NewAppSet(tc.entries))
				if err != nil || ok != tc.owned || (ok && pid != uint32(child.Process.Pid)) {
					t.Errorf("%s: FindOwner = (%d, %v, %v)，期望属于子进程 %d: %v", tc.name, pid, ok, err, child.Process.Pid, tc.owned)
				}
			}

			// 服务端这一侧的 socket 属于本进程。
			pid, ok, err := finder.FindOwner(remote, local, NewAppSet([]string{self}))
			if err != nil || !ok || pid != uint32(os.Getpid()) {
				t.Errorf("本进程持有的连接: FindOwner = (%d, %v, %v)", pid, ok, err)
			}
		})
	}
}

func TestFindOwnerEmptyAppSet(t *testing.T) {
	finder, err := NewOwnerFinder()
	if err != nil {
		t.Fatal(err)
	}
	conn := openOwnerTestConnection(t)
	local, remote := conn.LocalAddr(), conn.RemoteAddr()
	for _, apps := range []*AppSet{nil, NewAppSet(nil)} {
		if pid, ok, err := finder.FindOwner(local, remote, apps); ok || err != nil || pid != 0 {
			t.Fatalf("空名单: FindOwner = (%d, %v, %v)", pid, ok, err)
		}
	}
}

// TestFindOwnerHalfClosed：应用建连后马上关了写端（等服务端先开口的程序），连接仍算它持有。
func TestFindOwnerHalfClosed(t *testing.T) {
	finder, err := NewOwnerFinder()
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn := openOwnerTestConnection(t)
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if pid, ok, err := finder.FindOwner(conn.LocalAddr(), conn.RemoteAddr(), NewAppSet([]string{self})); !ok || err != nil || pid != uint32(os.Getpid()) {
		t.Fatalf("半关闭的连接: FindOwner = (%d, %v, %v)", pid, ok, err)
	}
}

// TestFindOwnerClosedConnection：应用已经关掉的连接。Linux、macOS 上进程不再持有 socket，查不到持有者；
// Windows 的连接表在连接关完之前仍记着创建它的进程，照旧算它的。
func TestFindOwnerClosedConnection(t *testing.T) {
	finder, err := NewOwnerFinder()
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn := openOwnerTestConnection(t)
	_ = conn.Close()
	pid, ok, _ := finder.FindOwner(conn.LocalAddr(), conn.RemoteAddr(), NewAppSet([]string{self}))
	if want := runtime.GOOS == "windows"; ok != want || (ok && pid != uint32(os.Getpid())) {
		t.Fatalf("已经关闭的连接: FindOwner = (%d, %v)，期望属于本进程: %v", pid, ok, want)
	}
}

func openOwnerTestConnection(t *testing.T) net.Conn {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := listener.Accept()
		if err == nil {
			accepted <- c
		}
		close(accepted)
	}()
	conn, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if server := <-accepted; server != nil {
		t.Cleanup(func() { _ = server.Close() })
	}
	return conn
}

// BenchmarkFindOwner 量每条透明连接多出的查找开销：名单里的进程（本进程）持有连接、不持有连接、名单里
// 的程序都没在运行（不必查连接本身），以及连接已经不在（被 RST，Linux 上要多读一次 /proc/net/tcp）。
func BenchmarkFindOwner(b *testing.B) {
	finder, err := NewOwnerFinder()
	if err != nil {
		b.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		b.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer listener.Close()
	conn, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	server, err := listener.Accept()
	if err != nil {
		b.Fatal(err)
	}
	defer server.Close()
	local, remote := conn.LocalAddr(), conn.RemoteAddr()
	_, _, childServer := startOwnerClient(b, listener, "sniffy-owner-client")
	childLocal, childRemote := childServer.RemoteAddr(), childServer.LocalAddr()
	gone := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}

	for _, bc := range []struct {
		name          string
		local, remote net.Addr
		apps          *AppSet
		owned         bool
	}{
		{name: "名单里的进程持有", local: local, remote: remote, apps: NewAppSet([]string{self}), owned: true},
		{name: "名单里的进程不持有", local: childLocal, remote: childRemote, apps: NewAppSet([]string{self})},
		{name: "名单里的程序没在运行", local: local, remote: remote, apps: NewAppSet([]string{"no-such-app"})},
		{name: "连接已经不在", local: gone, remote: local, apps: NewAppSet([]string{self})},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok, _ := finder.FindOwner(bc.local, bc.remote, bc.apps); ok != bc.owned {
					b.Fatalf("FindOwner 结果 %v，期望 %v", ok, bc.owned)
				}
			}
		})
	}
}
