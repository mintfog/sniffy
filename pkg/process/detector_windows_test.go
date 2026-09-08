// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

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
			"tasklist.exe":   "SNIFFY_PROCESS_TEST_TASKLIST",
			"netstat.exe":    "SNIFFY_PROCESS_TEST_NETSTAT",
			"powershell.exe": "SNIFFY_PROCESS_TEST_POWERSHELL",
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

func setupWindowsCommandFixtures(t *testing.T) {
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
	for _, name := range []string{"tasklist.exe", "netstat.exe", "powershell.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("SNIFFY_PROCESS_TEST_COMMANDS", "1")
	t.Setenv("SNIFFY_PROCESS_TEST_TASKLIST", "")
	t.Setenv("SNIFFY_PROCESS_TEST_NETSTAT", "")
	t.Setenv("SNIFFY_PROCESS_TEST_POWERSHELL", "")
}

func TestWindowsGetProcessByPIDCSV(t *testing.T) {
	setupWindowsCommandFixtures(t)
	tests := []struct {
		name     string
		output   string
		wantName string
	}{
		{name: "含逗号的进程名", output: `"client,debug.exe","42","Console","1","12,345 K"` + "\r\n", wantName: "client,debug.exe"},
		{name: "匹配目标PID", output: `"other.exe","7","Console","1","10 K"` + "\r\n" + `"client,debug.exe","42","Console","1","12,345 K"` + "\r\n", wantName: "client,debug.exe"},
		{name: "PID不匹配", output: `"other.exe","7","Console","1","10 K"` + "\r\n", wantName: "PID_42"},
		{name: "空输出", wantName: "PID_42"},
		{name: "提示信息", output: "INFO: No tasks are running which match the specified criteria.\r\n", wantName: "PID_42"},
		{name: "CSV格式错误", output: `"client,debug.exe,"42"`, wantName: "PID_42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SNIFFY_PROCESS_TEST_TASKLIST", tt.output)
			d, err := NewWindowsDetector()
			if err != nil {
				t.Fatal(err)
			}
			got, err := d.GetProcessByPID(42)
			if err != nil {
				t.Fatalf("GetProcessByPID(): %v", err)
			}
			if got == nil {
				t.Fatal("未获取到进程信息")
			}
			if got.PID != 42 || got.Name != tt.wantName {
				t.Fatalf("GetProcessByPID() = (%d, %q)，期望 (42, %q)", got.PID, got.Name, tt.wantName)
			}
			if got.IconData == "" {
				t.Fatal("进程信息缺少图标数据")
			}
		})
	}
}

func TestWindowsScopedIPv6Connection(t *testing.T) {
	setupWindowsCommandFixtures(t)
	t.Setenv("SNIFFY_PROCESS_TEST_TASKLIST", `"client,debug.exe","42","Console","1","12,345 K"`+"\r\n")
	t.Setenv("SNIFFY_PROCESS_TEST_NETSTAT", "TCP    [fe80::1%12]:51000    [fe80::2%12]:443    ESTABLISHED    42\r\n")
	d, err := NewWindowsDetector()
	if err != nil {
		t.Fatal(err)
	}
	local := &net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 51000, Zone: "12"}
	remote := &net.TCPAddr{IP: net.ParseIP("fe80::2"), Port: 443, Zone: "12"}
	got, err := d.GetProcessByConnection(local, remote)
	if err != nil {
		t.Fatalf("GetProcessByConnection(): %v", err)
	}
	if got == nil || got.PID != 42 || got.Name != "client,debug.exe" {
		t.Fatalf("GetProcessByConnection() = %+v", got)
	}
	connections, err := d.GetAllConnections()
	if err != nil {
		t.Fatalf("GetAllConnections(): %v", err)
	}
	if len(connections) != 1 {
		t.Fatalf("GetAllConnections() 返回 %d 条连接，期望 1 条", len(connections))
	}
	conn := connections[0]
	if conn.LocalAddr.String() != local.String() || conn.RemoteAddr.String() != remote.String() {
		t.Fatalf("连接地址 = (%v, %v)，期望 (%v, %v)", conn.LocalAddr, conn.RemoteAddr, local, remote)
	}
	if conn.ProcessInfo == nil || conn.ProcessInfo.PID != 42 || conn.ProcessInfo.Name != "client,debug.exe" {
		t.Fatalf("连接进程 = %+v", conn.ProcessInfo)
	}
}

func TestWindowsDetectorLifecycle(t *testing.T) {
	d, err := NewWindowsDetector()
	if err != nil {
		t.Fatalf("NewWindowsDetector(): %v", err)
	}
	if d.connections == nil || d.iconExtractor == nil || d.isRunning {
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

func TestParseWindowsTCPAddr(t *testing.T) {
	t.Parallel()
	d := &WindowsDetector{}
	tests := []struct {
		name     string
		input    string
		wantIP   string
		wantPort int
		wantZone string
		wantErr  bool
	}{
		{name: "IPv4", input: "127.0.0.1:8080", wantIP: "127.0.0.1", wantPort: 8080},
		{name: "IPv6", input: "[2001:db8::1]:443", wantIP: "2001:db8::1", wantPort: 443},
		{name: "IPv6 scope编号", input: "[fe80::1%12]:443", wantIP: "fe80::1", wantPort: 443, wantZone: "12"},
		{name: "IPv6 scope名称", input: "[fe80::1%Ethernet]:443", wantIP: "fe80::1", wantPort: 443, wantZone: "Ethernet"},
		{name: "wildcard", input: "*:*", wantIP: "0.0.0.0"},
		{name: "malformed wildcard", input: "*: *", wantErr: true},
		{name: "zero wildcard", input: "0.0.0.0:0", wantIP: "0.0.0.0"},
		{name: "invalid IP", input: "host.invalid:80", wantErr: true},
		{name: "missing port", input: "127.0.0.1", wantErr: true},
		{name: "overflowing port", input: "127.0.0.1:65536", wantErr: true},
		{name: "IPv6 scope端口溢出", input: "[fe80::1%12]:65536", wantErr: true},
		{name: "空scope", input: "[fe80::1%]:443", wantErr: true},
		{name: "IPv4 scope", input: "127.0.0.1%12:443", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, parse := range []func(string) (net.Addr, error){d.parseAddress, d.parseAddressSimple} {
				got, err := parse(tt.input)
				if tt.wantErr {
					if err == nil || got != nil {
						t.Fatalf("parse(%q) = (%v, %v)", tt.input, got, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("parse(%q): %v", tt.input, err)
				}
				tcp := got.(*net.TCPAddr)
				if tcp.IP.String() != tt.wantIP || tcp.Port != tt.wantPort || tcp.Zone != tt.wantZone {
					t.Fatalf("parse(%q) = %v，期望 IP=%s port=%d zone=%q", tt.input, got, tt.wantIP, tt.wantPort, tt.wantZone)
				}
			}
		})
	}
}

func TestParseWmicProcessOutput(t *testing.T) {
	t.Parallel()
	d := &WindowsDetector{}

	header := "Node,CommandLine,ExecutablePath,Name,ProcessId\r\n"
	row := `DESKTOP,"""C:\Apps\client,debug.exe"" --tag=a,b","C:\Apps\client,debug.exe","client,debug.exe",42` + "\r\n"
	want := ProcessInfo{PID: 42, Name: "client,debug.exe", Path: `C:\Apps\client,debug.exe`, CommandLine: `"C:\Apps\client,debug.exe" --tag=a,b`}
	tests := []struct {
		name   string
		output string
		want   ProcessInfo
	}{
		{name: "Node列与CSV转义", output: "\r\n" + header + row, want: want},
		{name: "匹配目标PID", output: header + "DESKTOP,run,C:\\Apps\\other.exe,other.exe,7\r\n" + row, want: want},
		{name: "从路径提取进程名", output: header + "DESKTOP,run,C:\\Apps\\fallback.exe,,42\r\n", want: ProcessInfo{PID: 42, Name: "fallback.exe", Path: `C:\Apps\fallback.exe`, CommandLine: "run"}},
		{name: "按列名定位", output: "Node,ProcessId,Name,ExecutablePath,CommandLine\r\nDESKTOP,42,client.exe,C:\\Apps\\client.exe,--serve\r\n", want: ProcessInfo{PID: 42, Name: "client.exe", Path: `C:\Apps\client.exe`, CommandLine: "--serve"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.parseWmicProcessOutput(tt.output, 42)
			if err != nil {
				t.Fatalf("parseWmicProcessOutput(): %v", err)
			}
			if got == nil || *got != tt.want {
				t.Fatalf("parseWmicProcessOutput() = %+v，期望 %+v", got, tt.want)
			}
		})
	}
	for name, output := range map[string]string{
		"空输出":      "",
		"只有标题":     header,
		"缺少必要列":    "Node,Name,ProcessId\r\nDESKTOP,client.exe,42\r\n",
		"PID不匹配":   header + "DESKTOP,run,C:\\Apps\\other.exe,other.exe,7\r\n",
		"PID格式错误":  header + "DESKTOP,run,C:\\Apps\\client.exe,client.exe,invalid\r\n",
		"数据行字段不完整": header + "DESKTOP,run\r\n",
		"CSV格式错误":  header + `DESKTOP,"unterminated`,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := d.parseWmicProcessOutput(output, 42); err == nil || got != nil {
				t.Fatalf("parseWmicProcessOutput() = (%+v, %v)，期望解析失败", got, err)
			}
		})
	}
}

func BenchmarkParseWindowsTCPAddr(b *testing.B) {
	for _, input := range []string{"127.0.0.1:8080", "[2001:db8::1]:443", "[fe80::1%12]:443"} {
		b.Run(input, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := parseWindowsTCPAddr(input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestParseTasklistOutput(t *testing.T) {
	t.Parallel()
	d := &WindowsDetector{}
	output := `"Image Name","PID","Session Name","Session#","Mem Usage","Status","User Name","CPU Time","Window Title"` + "\r\n" +
		`"client.exe","42","Console","1","12,345 K","Running","ACME\tester","0:00:01","Main Window"` + "\r\n"

	got, err := d.parseTasklistOutput(output, 42)
	if err != nil {
		t.Fatalf("parseTasklistOutput(): %v", err)
	}
	if got.PID != 42 || got.Name != "client.exe" || got.User != `ACME\tester` {
		t.Fatalf("parseTasklistOutput() = %+v", got)
	}
	if got, err := d.parseTasklistOutput(output, 7); err == nil || got != nil {
		t.Fatalf("parseTasklistOutput(missing) = (%+v, %v)", got, err)
	}
}

func TestParsePowerShellOutput(t *testing.T) {
	t.Parallel()
	d := &WindowsDetector{}
	output := "{\n  \"Name\": \"client\",\n  \"Path\": \"C:\\\\Apps\\\\client.exe\",\n  \"CommandLine\": \"--serve api\"\n}\n"
	got, err := d.parsePowerShellOutput(output, 42)
	if err != nil {
		t.Fatalf("parsePowerShellOutput(): %v", err)
	}
	if got.PID != 42 || got.Name != "client" || got.Path != `C:\Apps\client.exe` || got.CommandLine != "--serve api" {
		t.Fatalf("parsePowerShellOutput() = %+v", got)
	}
	if got, err := d.parsePowerShellOutput("not-json", 42); err == nil || got != nil {
		t.Fatalf("parsePowerShellOutput(invalid) = (%+v, %v)", got, err)
	}
}
