// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGetProcessByConnectionResolvesSelf(t *testing.T) {
	requireProcTCP(t)
	conn := openLoopbackConnectionOn(t, "tcp4", "127.0.0.1:0")

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

func TestLinuxDetectorResolvesHalfClosedConnection(t *testing.T) {
	requireProcTCP(t)
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			address := "127.0.0.1:0"
			if network == "tcp6" {
				address = "[::1]:0"
			}
			conn := openLoopbackConnectionOn(t, network, address)
			if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
				t.Fatal(err)
			}
			d, err := NewLinuxDetector()
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []struct {
				name          string
				local, remote net.Addr
			}{
				{name: "发起半关闭的一端", local: conn.LocalAddr(), remote: conn.RemoteAddr()},
				{name: "接收半关闭的一端", local: conn.RemoteAddr(), remote: conn.LocalAddr()},
			} {
				info, err := d.GetProcessByConnection(endpoint.local, endpoint.remote)
				if err != nil || info == nil || info.PID != uint32(os.Getpid()) {
					t.Errorf("%s的进程 = (%+v, %v)，期望 PID %d", endpoint.name, info, err, os.Getpid())
				}
			}
		})
	}
}

func TestLinuxDetectorLifecycle(t *testing.T) {
	d, err := NewLinuxDetector()
	if err != nil {
		t.Fatalf("NewLinuxDetector(): %v", err)
	}
	if d.procRoot != "/proc" || d.isRunning {
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
	conn := openLoopbackConnectionOn(t, "tcp4", "127.0.0.1:0")
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

func TestLinuxDetectorResolvesIPv6Connection(t *testing.T) {
	requireProcTCP(t)
	if _, err := os.Stat("/proc/net/tcp6"); err != nil {
		t.Skipf("/proc/net/tcp6 is unavailable: %v", err)
	}
	conn := openLoopbackConnectionOn(t, "tcp6", "[::1]:0")
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
	if got, err := d.findConnectionInode(netip.MustParseAddrPort("127.0.0.1:1"), netip.MustParseAddrPort("127.0.0.1:2")); err == nil || got != "" {
		t.Fatalf("未匹配连接的 inode = (%q, %v)", got, err)
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
	bigEndian := binary.NativeEndian.Uint32([]byte{0, 0, 0, 1}) == 1
	tests := []struct {
		name     string
		input    string
		inputBE  string
		wantIP   string
		wantPort int
		wantErr  bool
	}{
		{
			name: "IPv4", wantIP: "127.0.0.1", wantPort: 8080,
			input:   "0100007F:1F90",
			inputBE: "7F000001:1F90",
		},
		{
			name: "IPv6 loopback", wantIP: "::1", wantPort: 443,
			input:   "00000000000000000000000001000000:01BB",
			inputBE: "00000000000000000000000000000001:01BB",
		},
		{
			name: "IPv6 multi-word", wantIP: "2001:db8::1", wantPort: 8443,
			input:   "B80D0120000000000000000001000000:20FB",
			inputBE: "20010DB8000000000000000000000001:20FB",
		},
		{
			name: "IPv4 映射地址", wantIP: "127.0.0.1", wantPort: 8080,
			input:   "0000000000000000FFFF00000100007F:1F90",
			inputBE: "00000000000000000000FFFF7F000001:1F90",
		},
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
			if bigEndian && tt.inputBE != "" {
				tt.input = tt.inputBE
			}
			got, err := parseHexAddr(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseHexAddr(%q) = (%v, %v)，期望错误: %t", tt.input, got, err, tt.wantErr)
			}
			if tt.wantErr && got != nil {
				t.Fatalf("parseHexAddr(%q) 同时返回了地址 %v 与错误 %v", tt.input, got, err)
			}
			if tt.wantErr {
				return
			}
			if got == nil || got.IP.String() != tt.wantIP || got.Port != tt.wantPort {
				t.Fatalf("parseHexAddr(%q) = %v, want %s:%d", tt.input, got, tt.wantIP, tt.wantPort)
			}
		})
	}
}

func TestParseNetLine(t *testing.T) {
	t.Parallel()

	if got, err := parseNetLine("too few fields", "tcp"); err == nil || got != nil {
		t.Fatalf("parseNetLine(short) = (%+v, %v)", got, err)
	}
	nonEstablished := "0: invalid invalid 0A 0 0 0 0 0 0"
	for _, protocol := range []string{"tcp", "tcp6"} {
		if got, err := parseNetLine(nonEstablished, protocol); err != nil || got != nil {
			t.Fatalf("parseNetLine(%s LISTEN) = (%+v, %v)", protocol, got, err)
		}
	}

	for name, line := range map[string]string{
		"本地地址非法": "0: bogus 0100007F:01BB 01 0 0 0 0 0 1",
		"远端地址非法": "0: 0100007F:01BB bogus 01 0 0 0 0 0 1",
	} {
		if got, err := parseNetLine(line, "tcp"); err == nil || got != nil {
			t.Fatalf("parseNetLine(%s) = (%+v, %v)，期望解析失败", name, got, err)
		}
	}

	loopback := binary.NativeEndian.Uint32([]byte{127, 0, 0, 1})
	line := fmt.Sprintf("0: %08X:C738 %08X:01BB 01 0 0 0 0 0 123", loopback, loopback)
	got, err := parseNetLine(line, "tcp")
	if err != nil {
		t.Fatalf("parseNetLine(): %v", err)
	}
	if got.Protocol != "TCP" || got.inode != "123" {
		t.Fatalf("parseNetLine() = %+v", got)
	}
	if got.LocalAddr.String() != "127.0.0.1:51000" || got.RemoteAddr.String() != "127.0.0.1:443" {
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
	f.Fuzz(func(t *testing.T, value string) {
		got, err := parseHexAddr(value)
		if err != nil {
			if got != nil {
				t.Fatalf("parseHexAddr(%q) 同时返回了地址 %v 与错误 %v", value, got, err)
			}
			return
		}
		if got == nil {
			t.Fatalf("parseHexAddr(%q) 返回空地址", value)
		}
		if len(got.IP) != net.IPv4len && len(got.IP) != net.IPv6len {
			t.Fatalf("parseHexAddr(%q) IP 宽度 = %d 字节", value, len(got.IP))
		}
		if got.Port < 0 || got.Port > 65535 {
			t.Fatalf("parseHexAddr(%q) 端口 = %d 越界", value, got.Port)
		}
	})
}

var benchmarkAddr net.Addr

func BenchmarkParseHexAddr(b *testing.B) {
	for name, input := range map[string]string{
		"IPv4": "0100007F:1F90",
		"IPv6": "00000000000000000000000001000000:01BB",
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				got, err := parseHexAddr(input)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkAddr = got
			}
		})
	}
}

func requireProcTCP(t testing.TB) {
	t.Helper()
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skipf("/proc/net/tcp is unavailable: %v", err)
	}
}

func openLoopbackConnectionOn(t testing.TB, network, address string) net.Conn {
	t.Helper()
	ln, err := net.Listen(network, address)
	if err != nil {
		t.Skipf("listen %s %s: %v", network, address, err)
	}
	t.Cleanup(func() { ln.Close() })
	if err := ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTimeout(network, ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	t.Cleanup(func() { server.Close() })
	return client
}

func TestLinuxConnectionTableMatching(t *testing.T) {
	d, _ := NewLinuxDetector()
	d.procRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Join(d.procRoot, "net"), 0700); err != nil {
		t.Fatal(err)
	}
	loopback := binary.NativeEndian.Uint32([]byte{127, 0, 0, 1})
	otherIP := binary.NativeEndian.Uint32([]byte{127, 0, 0, 2})
	rows := "header\n" +
		fmt.Sprintf("0: %08X:C738 %08X:01BB 01 0 0 0 0 0 111\n", otherIP, loopback) +
		fmt.Sprintf("1: %08X:C738 %08X:01BB 06 0 0 0 0 0 0\n", loopback, loopback) +
		fmt.Sprintf("2: %08X:C738 %08X:01BB 01 0 0 0 0 0 333\n", loopback, loopback)
	if err := os.WriteFile(filepath.Join(d.procRoot, "net", "tcp"), []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	for pid, target := range map[string]string{"10": "pipe:[333]", "20": "socket:[333]"} {
		fdDir := filepath.Join(d.procRoot, pid, "fd")
		if err := os.MkdirAll(fdDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(fdDir, "3")); err != nil {
			t.Fatal(err)
		}
	}
	local, remote := netip.MustParseAddrPort("127.0.0.1:51000"), netip.MustParseAddrPort("127.0.0.1:443")
	inode, err := d.findConnectionInode(local, remote)
	if err != nil || inode != "333" {
		t.Fatalf("匹配连接 inode = (%q, %v)，期望 333", inode, err)
	}
	pid, err := d.findProcessByInode(inode)
	if err != nil || pid != 20 {
		t.Fatalf("socket 所属进程 = (%d, %v)，期望 20", pid, err)
	}
	connections, err := d.GetAllConnections()
	if err != nil || len(connections) != 1 || connections[0].ProcessInfo.PID != 20 {
		t.Fatalf("批量关联连接 = (%+v, %v)", connections, err)
	}
}

func BenchmarkLinuxConnectionLookup(b *testing.B) {
	requireProcTCP(b)
	conn := openLoopbackConnectionOn(b, "tcp4", "127.0.0.1:0")
	d, _ := NewLinuxDetector()
	b.ReportAllocs()
	for b.Loop() {
		info, err := d.GetProcessByConnection(conn.LocalAddr(), conn.RemoteAddr())
		if err != nil || info == nil || info.PID != uint32(os.Getpid()) {
			b.Fatalf("连接所属进程 = (%+v, %v)", info, err)
		}
	}
}

func BenchmarkLinuxAllConnections(b *testing.B) {
	requireProcTCP(b)
	for range 24 {
		openLoopbackConnectionOn(b, "tcp4", "127.0.0.1:0")
	}
	d, _ := NewLinuxDetector()
	b.ReportAllocs()
	for b.Loop() {
		connections, err := d.GetAllConnections()
		if err != nil || len(connections) < 48 {
			b.Fatalf("批量查询得到 %d 条连接: %v", len(connections), err)
		}
	}
}
