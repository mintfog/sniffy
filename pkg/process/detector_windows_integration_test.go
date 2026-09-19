// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func windowsTestConnection(t testing.TB, network, address string) net.Conn {
	t.Helper()
	listener, err := net.Listen(network, address)
	if err != nil {
		if network == "tcp6" && (errors.Is(err, windows.WSAEAFNOSUPPORT) || errors.Is(err, windows.WSAEADDRNOTAVAIL)) {
			t.Skipf("当前系统未启用 IPv6: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTimeout(network, listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return client
}

func TestWindowsConnectionLookup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			address := "127.0.0.1:0"
			if network == "tcp6" {
				address = "[::1]:0"
			}
			client := windowsTestConnection(t, network, address)
			d, err := NewWindowsDetector()
			if err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					got, err := d.GetProcessByConnection(client.LocalAddr(), client.RemoteAddr())
					if err != nil || got == nil || got.PID != uint32(os.Getpid()) || !strings.EqualFold(got.Path, executable) {
						t.Errorf("连接所属进程 = (%+v, %v)，期望当前进程", got, err)
					}
				})
			}
			wg.Wait()
			wrong := *client.RemoteAddr().(*net.TCPAddr)
			wrong.IP = net.ParseIP("127.0.0.2")
			if network == "tcp6" {
				wrong.IP = net.ParseIP("::2")
			}
			if got, err := d.GetProcessByConnection(client.LocalAddr(), &wrong); err == nil || got != nil {
				t.Fatalf("相同端口但不同 IP 不应匹配: (%+v, %v)", got, err)
			}
		})
	}
}

func TestWindowsAllConnections(t *testing.T) {
	d, err := NewWindowsDetector()
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]bool)
	for range 24 {
		client := windowsTestConnection(t, "tcp4", "127.0.0.1:0")
		want[client.LocalAddr().String()+"/"+client.RemoteAddr().String()] = true
	}
	connections, err := d.GetAllConnections()
	if err != nil {
		t.Fatal(err)
	}
	for _, conn := range connections {
		key := conn.LocalAddr.String() + "/" + conn.RemoteAddr.String()
		if want[key] {
			if conn.Protocol != "TCP" || conn.ProcessInfo == nil || conn.ProcessInfo.PID != uint32(os.Getpid()) {
				t.Fatalf("连接 %s 的进程信息错误: %+v", key, conn)
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("遗漏了 %d 条测试连接", len(want))
	}
}

func BenchmarkWindowsConnectionLookup(b *testing.B) {
	client := windowsTestConnection(b, "tcp4", "127.0.0.1:0")
	d, err := NewWindowsDetector()
	if err != nil {
		b.Fatal(err)
	}
	local, remote := client.LocalAddr(), client.RemoteAddr()
	b.ReportAllocs()
	for b.Loop() {
		got, err := d.GetProcessByConnection(local, remote)
		if err != nil || got == nil || got.PID != uint32(os.Getpid()) || got.Path == "" {
			b.Fatalf("连接所属进程 = (%+v, %v)", got, err)
		}
	}
}
