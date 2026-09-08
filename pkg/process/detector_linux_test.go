// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// 单次查询限制为 2 秒，用于检测 O(连接数 × 进程数) 全量扫描造成的性能退化。
func TestGetProcessByConnectionResolvesSelf(t *testing.T) {
	requireProcTCP(t)
	conn := openLoopbackConnection(t)

	d, err := NewDetector()
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}

	var pi *ProcessInfo
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		start := time.Now()
		pi, err = d.GetProcessByConnection(conn.LocalAddr(), conn.RemoteAddr())
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("单次连接查询耗时 %v，超过 2 秒上限", elapsed)
		}
		if err == nil {
			t.Logf("单次连接查询耗时 %v", time.Since(start))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GetProcessByConnection: %v", err)
	}
	if pi == nil {
		t.Fatal("未获取到进程信息")
	}
	if pi.PID != uint32(os.Getpid()) {
		t.Errorf("PID = %d, want %d (%q)", pi.PID, os.Getpid(), pi.Name)
	}
	if pi.Name == "" || pi.Path == "" || pi.CommandLine == "" || pi.User == "" {
		t.Errorf("incomplete process info: %+v", pi)
	}
}

func TestLinuxDetectorLifecycle(t *testing.T) {
	d, err := NewLinuxDetector()
	if err != nil {
		t.Fatalf("NewLinuxDetector(): %v", err)
	}
	if d.connections == nil || d.isRunning {
		t.Fatalf("initial detector state = %+v", d)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("second Start(): %v", err)
	}
	if !d.isRunning {
		t.Fatal("detector is not running after Start")
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("second Stop(): %v", err)
	}
	if d.isRunning {
		t.Fatal("detector is running after Stop")
	}

	// 并发调用配合 -race 检测 isRunning 的同步问题。
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = d.Start()
		}()
		go func() {
			defer wg.Done()
			_ = d.Stop()
		}()
	}
	wg.Wait()
}

func TestLinuxDetectorGetProcessByPID(t *testing.T) {
	requireProcTCP(t)
	d, err := NewLinuxDetector()
	if err != nil {
		t.Fatalf("NewLinuxDetector(): %v", err)
	}

	got, err := d.GetProcessByPID(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("GetProcessByPID(self): %v", err)
	}
	if got.PID != uint32(os.Getpid()) || got.Name == "" || got.Path == "" || got.CommandLine == "" || got.User == "" {
		t.Fatalf("GetProcessByPID(self) = %+v", got)
	}
	if got, err := d.GetProcessByPID(^uint32(0)); err == nil || got != nil {
		t.Fatalf("GetProcessByPID(missing) = (%+v, %v)", got, err)
	}
}

func TestLinuxDetectorGetAllConnectionsIncludesSelf(t *testing.T) {
	requireProcTCP(t)
	if _, err := os.Stat("/proc/net/tcp6"); err != nil {
		t.Skipf("/proc/net/tcp6 is unavailable: %v", err)
	}
	conn := openLoopbackConnection(t)
	d, err := NewLinuxDetector()
	if err != nil {
		t.Fatalf("NewLinuxDetector(): %v", err)
	}

	wantLocal := conn.LocalAddr().String()
	wantRemote := conn.RemoteAddr().String()
	connections, err := d.GetAllConnections()
	if err != nil {
		t.Fatalf("GetAllConnections(): %v", err)
	}
	for _, got := range connections {
		if got.LocalAddr.String() == wantLocal && got.RemoteAddr.String() == wantRemote {
			if got.ProcessInfo == nil || got.ProcessInfo.PID != uint32(os.Getpid()) {
				t.Fatalf("matching connection has unexpected process: %+v", got.ProcessInfo)
			}
			return
		}
	}
	t.Fatalf("GetAllConnections() did not include %s -> %s", wantLocal, wantRemote)
}

// 使用真实 IPv6 连接，验证 /proc/net/tcp6 地址解码与 inode 到 PID 的关联。
func TestLinuxDetectorResolvesIPv6Connection(t *testing.T) {
	requireProcTCP(t)
	if _, err := os.Stat("/proc/net/tcp6"); err != nil {
		t.Skipf("/proc/net/tcp6 is unavailable: %v", err)
	}
	conn := openLoopbackConnection6(t)
	d, err := NewLinuxDetector()
	if err != nil {
		t.Fatalf("NewLinuxDetector(): %v", err)
	}

	var pi *ProcessInfo
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pi, err = d.GetProcessByConnection(conn.LocalAddr(), conn.RemoteAddr()); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GetProcessByConnection(IPv6): %v", err)
	}
	if pi == nil || pi.PID != uint32(os.Getpid()) {
		t.Fatalf("GetProcessByConnection(IPv6) = %+v，期望 PID %d", pi, os.Getpid())
	}

	wantLocal := conn.LocalAddr().String()
	wantRemote := conn.RemoteAddr().String()
	connections, err := d.GetAllConnections()
	if err != nil {
		t.Fatalf("GetAllConnections(): %v", err)
	}
	for _, got := range connections {
		if got.LocalAddr.String() != wantLocal || got.RemoteAddr.String() != wantRemote {
			continue
		}
		if got.Protocol != "TCP6" {
			t.Fatalf("Protocol = %q，期望 TCP6", got.Protocol)
		}
		if got.ProcessInfo == nil || got.ProcessInfo.PID != uint32(os.Getpid()) {
			t.Fatalf("matching connection has unexpected process: %+v", got.ProcessInfo)
		}
		return
	}
	t.Fatalf("GetAllConnections() did not include %s -> %s", wantLocal, wantRemote)
}

func TestLinuxInodeLookupMisses(t *testing.T) {
	requireProcTCP(t)
	d, err := NewLinuxDetector()
	if err != nil {
		t.Fatalf("NewLinuxDetector(): %v", err)
	}

	// 此用例假定宿主没有本地端口为 1、远端端口为 2 的连接。
	if got := d.findInodeByPorts(1, 2); got != "" {
		t.Fatalf("findInodeByPorts(1, 2) = %q，期望空", got)
	}
	// 此用例假定宿主未分配该 inode。
	if pid, err := d.findProcessByInode("999999999999999999"); err == nil || pid != 0 {
		t.Fatalf("findProcessByInode(missing) = (%d, %v)，期望失败", pid, err)
	}
}

func TestLinuxDetectorRejectsInvalidConnection(t *testing.T) {
	t.Parallel()
	d, err := NewLinuxDetector()
	if err != nil {
		t.Fatalf("NewLinuxDetector(): %v", err)
	}

	tests := []struct {
		name   string
		local  net.Addr
		remote net.Addr
	}{
		{name: "nil local", remote: &net.TCPAddr{Port: 443}},
		{name: "nil remote", local: &net.TCPAddr{Port: 8080}},
		{name: "zero local port", local: &net.TCPAddr{}, remote: &net.TCPAddr{Port: 443}},
		{name: "malformed address", local: stringAddr("invalid"), remote: stringAddr("also-invalid")},
		// 使用合法端口覆盖查找未命中的分支，假定宿主没有该端口对的连接。
		{name: "端口不对应任何 socket", local: &net.TCPAddr{Port: 1}, remote: &net.TCPAddr{Port: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, err := d.GetProcessByConnection(tt.local, tt.remote); err == nil || got != nil {
				t.Fatalf("GetProcessByConnection() = (%+v, %v)", got, err)
			}
		})
	}
}

func TestHexPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{input: "0100007F:0016", want: 22},
		{input: "00000000000000000000000001000000:FFFF", want: 65535},
		{input: "missing", want: -1},
		{input: "trailing:", want: -1},
		{input: "address:xyz", want: -1},
	}
	for _, tt := range tests {
		if got := hexPort(tt.input); got != tt.want {
			t.Errorf("hexPort(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseHexAddr(t *testing.T) {
	t.Parallel()
	d := &LinuxDetector{}
	tests := []struct {
		name     string
		input    string
		wantIP   string
		wantPort int
		wantErr  bool
	}{
		{name: "IPv4", input: "0100007F:1F90", wantIP: "127.0.0.1", wantPort: 8080},
		{name: "IPv6 loopback", input: "00000000000000000000000001000000:01BB", wantIP: "::1", wantPort: 443},
		{name: "IPv6 multi-word", input: "B80D0120000000000000000001000000:20FB", wantIP: "2001:db8::1", wantPort: 8443},
		{name: "IPv4 映射地址", input: "0000000000000000FFFF00000100007F:1F90", wantIP: "127.0.0.1", wantPort: 8080},
		{name: "IPv6 非法十六进制", input: strings.Repeat("G", 32) + ":0050", wantErr: true},
		{name: "missing separator", input: "0100007F1F90", wantErr: true},
		{name: "short IP", input: "01:0050", wantErr: true},
		{name: "invalid IP", input: "GG00007F:0050", wantErr: true},
		{name: "invalid port", input: "0100007F:GG", wantErr: true},
		{name: "overflowing port", input: "0100007F:10000", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := d.parseHexAddr(tt.input)
			if tt.wantErr {
				if err == nil || got != nil {
					t.Fatalf("parseHexAddr(%q) = (%v, %v)", tt.input, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHexAddr(%q): %v", tt.input, err)
			}
			tcp, ok := got.(*net.TCPAddr)
			if !ok || tcp.IP.String() != tt.wantIP || tcp.Port != tt.wantPort {
				t.Fatalf("parseHexAddr(%q) = %v, want %s:%d", tt.input, got, tt.wantIP, tt.wantPort)
			}
		})
	}
}

func TestParseNetLine(t *testing.T) {
	requireProcTCP(t)
	d, err := NewLinuxDetector()
	if err != nil {
		t.Fatalf("NewLinuxDetector(): %v", err)
	}

	if got, err := d.parseNetLine("too few fields", "tcp"); err == nil || got != nil {
		t.Fatalf("parseNetLine(short) = (%+v, %v)", got, err)
	}
	nonEstablished := "0: invalid invalid 0A 0 0 0 0 0 0"
	for _, protocol := range []string{"tcp", "tcp6"} {
		if got, err := d.parseNetLine(nonEstablished, protocol); err != nil || got != nil {
			t.Fatalf("parseNetLine(%s LISTEN) = (%+v, %v)", protocol, got, err)
		}
	}

	// 使用合成行覆盖已建立连接的地址损坏分支。
	for name, line := range map[string]string{
		"本地地址非法": "0: bogus 0100007F:01BB 01 0 0 0 0 0 1",
		"远端地址非法": "0: 0100007F:01BB bogus 01 0 0 0 0 0 1",
	} {
		if got, err := d.parseNetLine(line, "tcp"); err == nil || got != nil {
			t.Fatalf("parseNetLine(%s) = (%+v, %v)，期望解析失败", name, got, err)
		}
	}

	conn := openLoopbackConnection(t)
	localPort := conn.LocalAddr().(*net.TCPAddr).Port
	remotePort := conn.RemoteAddr().(*net.TCPAddr).Port
	inode := waitForInode(t, d, localPort, remotePort)
	if inode == "" {
		t.Fatal("findInodeByPorts() did not find the test socket")
	}
	line := fmt.Sprintf("0: 0100007F:%04X 0100007F:%04X 01 0 0 0 0 0 %s", localPort, remotePort, inode)
	got, err := d.parseNetLine(line, "tcp")
	if err != nil {
		t.Fatalf("parseNetLine(): %v", err)
	}
	if got.Protocol != "TCP" || got.ProcessInfo == nil || got.ProcessInfo.PID != uint32(os.Getpid()) {
		t.Fatalf("parseNetLine() = %+v", got)
	}
	if got.LocalAddr.String() != fmt.Sprintf("127.0.0.1:%d", localPort) || got.RemoteAddr.String() != fmt.Sprintf("127.0.0.1:%d", remotePort) {
		t.Fatalf("parseNetLine() addresses = (%v, %v)", got.LocalAddr, got.RemoteAddr)
	}
}

func FuzzParseHexAddr(f *testing.F) {
	for _, seed := range []string{
		"0100007F:1F90",
		"00000000000000000000000001000000:01BB",
		"B80D0120000000000000000001000000:20FB",
		"",
		strings.Repeat("F", 64),
	} {
		f.Add(seed)
	}
	d := &LinuxDetector{}
	f.Fuzz(func(t *testing.T, value string) {
		got, err := d.parseHexAddr(value)
		if err != nil {
			if got != nil {
				t.Fatalf("parseHexAddr(%q) 同时返回了地址 %v 与错误 %v", value, got, err)
			}
			return
		}
		tcp, ok := got.(*net.TCPAddr)
		if !ok {
			t.Fatalf("parseHexAddr(%q) 返回 %T，期望 *net.TCPAddr", value, got)
		}
		if len(tcp.IP) != net.IPv4len && len(tcp.IP) != net.IPv6len {
			t.Fatalf("parseHexAddr(%q) IP 宽度 = %d 字节", value, len(tcp.IP))
		}
		if tcp.Port < 0 || tcp.Port > 65535 {
			t.Fatalf("parseHexAddr(%q) 端口 = %d 越界", value, tcp.Port)
		}
	})
}

var benchmarkAddr net.Addr

func BenchmarkParseHexAddr(b *testing.B) {
	d := &LinuxDetector{}
	for name, input := range map[string]string{
		"IPv4": "0100007F:1F90",
		"IPv6": "00000000000000000000000001000000:01BB",
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				got, err := d.parseHexAddr(input)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkAddr = got
			}
		})
	}
}

func requireProcTCP(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skipf("/proc/net/tcp is unavailable: %v", err)
	}
}

func openLoopbackConnection(t *testing.T) net.Conn {
	t.Helper()
	return openLoopbackConnectionOn(t, "tcp4", "127.0.0.1:0")
}

func openLoopbackConnection6(t *testing.T) net.Conn {
	t.Helper()
	return openLoopbackConnectionOn(t, "tcp6", "[::1]:0")
}

func openLoopbackConnectionOn(t *testing.T, network, address string) net.Conn {
	t.Helper()
	ln, err := net.Listen(network, address)
	if err != nil {
		// 监听依赖宿主网络能力，例如 IPv6 环回可能被禁用。
		t.Skipf("listen %s %s: %v", network, address, err)
	}
	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	client, err := net.Dial(network, ln.Addr().String())
	if err != nil {
		ln.Close()
		t.Fatalf("dial: %v", err)
	}
	var server net.Conn
	select {
	case server = <-accepted:
	case err := <-acceptErr:
		client.Close()
		ln.Close()
		t.Fatalf("accept: %v", err)
	case <-time.After(time.Second):
		client.Close()
		ln.Close()
		t.Fatal("accept timed out")
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
		ln.Close()
	})
	return client
}

func waitForInode(t *testing.T, d *LinuxDetector, localPort, remotePort int) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inode := d.findInodeByPorts(localPort, remotePort); inode != "" {
			return inode
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}
