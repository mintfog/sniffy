// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMain(m *testing.M) {
	// 子进程使用测试二进制模拟系统命令，让公开查询入口消费固定的原始输出。
	if os.Getenv("SNIFFY_PROCESS_TEST_COMMANDS") == "1" {
		executable, err := os.Executable()
		if err != nil {
			os.Exit(1)
		}
		outputs := map[string]string{
			"lsof": "SNIFFY_PROCESS_TEST_LSOF",
			"ps":   "SNIFFY_PROCESS_TEST_PS",
		}
		// 替身进程继承测试环境和 PATH；未知命令直接退出，避免重跑测试递归派生子进程。
		key, ok := outputs[filepath.Base(executable)]
		if !ok {
			fmt.Fprintf(os.Stderr, "未桩的命令替身: %s\n", executable)
			os.Exit(1)
		}
		fmt.Print(os.Getenv(key))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func setupDarwinCommandFixtures(t *testing.T) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"lsof", "ps"} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("SNIFFY_PROCESS_TEST_COMMANDS", "1")
	t.Setenv("SNIFFY_PROCESS_TEST_LSOF", "")
	t.Setenv("SNIFFY_PROCESS_TEST_PS", "")
}

const lsofHeader = "COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\n"

const psFixture = "  PID COMM ARGS\n   42 /usr/bin/client /usr/bin/client --serve api\n"

func TestDarwinDetectorLifecycle(t *testing.T) {
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatalf("NewDarwinDetector(): %v", err)
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

func TestParsePsOutput(t *testing.T) {
	t.Parallel()
	d := &DarwinDetector{}
	output := "  PID COMM ARGS\n   7 /usr/bin/other /usr/bin/other --flag\n  42 /usr/bin/client /usr/bin/client --serve api\n"

	got, err := d.parsePsOutput(output, 42)
	if err != nil {
		t.Fatalf("parsePsOutput(): %v", err)
	}
	if got.PID != 42 || got.Name != "/usr/bin/client" || got.Path != "/usr/bin/client" || got.CommandLine != "/usr/bin/client --serve api" {
		t.Fatalf("parsePsOutput() = %+v", got)
	}
	for name, invalid := range map[string]string{
		"missing PID": output,
		"malformed":   "PID COMM ARGS\nnot-a-pid client command\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := d.parsePsOutput(invalid, 99); err == nil || got != nil {
				t.Fatalf("parsePsOutput() = (%+v, %v)", got, err)
			}
		})
	}
}

func TestDarwinMatchConnection(t *testing.T) {
	t.Parallel()
	d := &DarwinDetector{}
	conn := &ConnectionProcess{
		LocalAddr:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 51000},
		RemoteAddr: &net.TCPAddr{IP: net.ParseIP("203.0.113.1"), Port: 443},
	}

	if !d.matchConnection(conn, &net.TCPAddr{Port: 51000}, &net.TCPAddr{Port: 443}) {
		t.Fatal("matchConnection() rejected matching ports")
	}
	if d.matchConnection(conn, &net.TCPAddr{Port: 51001}, &net.TCPAddr{Port: 443}) {
		t.Fatal("matchConnection() accepted a different local port")
	}
	if d.matchConnection(conn, &net.TCPAddr{Port: 51000}, &net.TCPAddr{Port: 80}) {
		t.Fatal("matchConnection() accepted a different remote port")
	}
	if d.matchConnection(&ConnectionProcess{}, nil, nil) {
		t.Fatal("matchConnection() accepted missing connection addresses")
	}
}

func TestParseLsofOutput(t *testing.T) {
	// ps 替身返回空输出，使进程查询稳定进入 lsof 行内信息的回退分支。
	setupDarwinCommandFixtures(t)
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatalf("NewDarwinDetector(): %v", err)
	}
	output := lsofHeader +
		"malformed\n" +
		"client 4294967295 tester 12u IPv4 0x123 0t0 TCP 127.0.0.1:51000->203.0.113.1:443 (ESTABLISHED)\n" +
		"client 42 tester 13u IPv4 0x124 0t0 TCP *:8080 (LISTEN)\n"
	got, err := d.parseLsofOutput(output)
	if err != nil {
		t.Fatalf("parseLsofOutput(): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("parseLsofOutput() returned %d connections, want 1", len(got))
	}
	conn := got[0]
	if conn.Protocol != "TCP" || conn.LocalAddr.String() != "127.0.0.1:51000" || conn.RemoteAddr.String() != "203.0.113.1:443" {
		t.Fatalf("parseLsofOutput() = %+v", conn)
	}
	if conn.ProcessInfo == nil || conn.ProcessInfo.PID != ^uint32(0) || conn.ProcessInfo.Name != "client" || conn.ProcessInfo.User != "tester" {
		t.Fatalf("process info = %+v", conn.ProcessInfo)
	}
}

// lsof 的协议位于 NODE 列、地址位于 NAME 列；SIZE/OFF 缺失时后续列会左移。
func TestDarwinLsofDrivenQueries(t *testing.T) {
	setupDarwinCommandFixtures(t)
	t.Setenv("SNIFFY_PROCESS_TEST_PS", psFixture)
	t.Setenv("SNIFFY_PROCESS_TEST_LSOF", lsofHeader+
		"client 42 tester 5u KQUEUE 0x1 0t0 count=0 state=0xa\n"+
		"client 42 tester 12u IPv4 0x123 0t0 TCP 127.0.0.1:51000->203.0.113.1:443 (ESTABLISHED)\n"+
		"client 42 tester 14u IPv4 0xdef 0t0 TCP *:8080 (LISTEN)\n"+
		"client 42 tester 13u IPv6 0xabc TCP [::1]:51001->[::1]:8443 (ESTABLISHED)\n")

	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatalf("NewDarwinDetector(): %v", err)
	}

	wantInfo := ProcessInfo{PID: 42, Name: "/usr/bin/client", Path: "/usr/bin/client", CommandLine: "/usr/bin/client --serve api"}
	got, err := d.GetProcessByConnection(&net.TCPAddr{Port: 51000}, &net.TCPAddr{Port: 443})
	if err != nil {
		t.Fatalf("GetProcessByConnection(): %v", err)
	}
	if got == nil || *got != wantInfo {
		t.Fatalf("GetProcessByConnection() = %+v，期望 %+v", got, wantInfo)
	}

	got, err = d.GetProcessByConnection(&net.TCPAddr{Port: 51001}, &net.TCPAddr{Port: 8443})
	if err != nil {
		t.Fatalf("GetProcessByConnection(列左移): %v", err)
	}
	if got == nil || *got != wantInfo {
		t.Fatalf("GetProcessByConnection(列左移) = %+v", got)
	}

	if got, err := d.GetProcessByConnection(&net.TCPAddr{Port: 51000}, &net.TCPAddr{Port: 9999}); err == nil || got != nil {
		t.Fatalf("GetProcessByConnection(远端端口不匹配) = (%+v, %v)", got, err)
	}
	if got, err := d.GetProcessByConnection(&net.TCPAddr{}, &net.TCPAddr{Port: 443}); err == nil || got != nil {
		t.Fatalf("GetProcessByConnection(本地端口为 0) = (%+v, %v)", got, err)
	}

	connections, err := d.GetAllConnections()
	if err != nil {
		t.Fatalf("GetAllConnections(): %v", err)
	}
	if len(connections) != 2 {
		t.Fatalf("GetAllConnections() 返回 %d 条连接，期望 2 条", len(connections))
	}
	for i, want := range []struct {
		local  string
		remote string
	}{
		{local: "127.0.0.1:51000", remote: "203.0.113.1:443"},
		{local: "[::1]:51001", remote: "[::1]:8443"},
	} {
		conn := connections[i]
		if conn.Protocol != "TCP" || conn.LocalAddr.String() != want.local || conn.RemoteAddr.String() != want.remote {
			t.Fatalf("连接[%d] = (%s, %v, %v)，期望 (TCP, %s, %s)", i, conn.Protocol, conn.LocalAddr, conn.RemoteAddr, want.local, want.remote)
		}
		if conn.ProcessInfo == nil || *conn.ProcessInfo != wantInfo {
			t.Fatalf("连接[%d] 进程 = %+v", i, conn.ProcessInfo)
		}
	}
}

func TestDarwinGetProcessByPID(t *testing.T) {
	setupDarwinCommandFixtures(t)
	t.Setenv("SNIFFY_PROCESS_TEST_PS", psFixture)
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatalf("NewDarwinDetector(): %v", err)
	}

	got, err := d.GetProcessByPID(42)
	if err != nil {
		t.Fatalf("GetProcessByPID(): %v", err)
	}
	want := ProcessInfo{PID: 42, Name: "/usr/bin/client", Path: "/usr/bin/client", CommandLine: "/usr/bin/client --serve api"}
	if got == nil || *got != want {
		t.Fatalf("GetProcessByPID() = %+v，期望 %+v", got, want)
	}

	if got, err := d.GetProcessByPID(7); err == nil || got != nil {
		t.Fatalf("GetProcessByPID(未列出的 PID) = (%+v, %v)", got, err)
	}
	t.Setenv("SNIFFY_PROCESS_TEST_PS", "")
	if got, err := d.GetProcessByPID(42); err == nil || got != nil {
		t.Fatalf("GetProcessByPID(ps 输出为空) = (%+v, %v)", got, err)
	}
}

func TestParseLsofAddr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		wantIP   string
		wantPort int
		wantErr  bool
	}{
		{input: "127.0.0.1:8080", wantIP: "127.0.0.1", wantPort: 8080},
		{input: "[2001:db8::1]:443", wantIP: "2001:db8::1", wantPort: 443},
		{input: "host.invalid:80", wantErr: true},
		{input: "127.0.0.1", wantErr: true},
		{input: "127.0.0.1:65536", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, err := parseLsofAddr(tt.input)
			if tt.wantErr {
				if err == nil || got != nil {
					t.Fatalf("parseLsofAddr(%q) = (%v, %v)", tt.input, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLsofAddr(%q): %v", tt.input, err)
			}
			if got.IP.String() != tt.wantIP || got.Port != tt.wantPort {
				t.Fatalf("parseLsofAddr(%q) = %v, want %s:%d", tt.input, got, tt.wantIP, tt.wantPort)
			}
		})
	}
}
