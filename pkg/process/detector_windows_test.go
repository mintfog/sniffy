// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsGetProcessByPID(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	d, err := NewWindowsDetector()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := d.GetProcessByPID(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if !sameFile(info.Path, executable) || info.Name != filepath.Base(info.Path) || info.User == "" || info.IconData == "" {
		t.Fatalf("当前进程信息 = %+v，期望路径 %q，且用户和图标完整", info, executable)
	}
	if name := windowsProcessName(uint32(os.Getpid())); name != filepath.Base(executable) {
		t.Fatalf("进程快照名称 = %q，期望 %q", name, filepath.Base(executable))
	}
	for _, pid := range []uint32{0, ^uint32(0)} {
		info, err := d.GetProcessByPID(pid)
		if err != nil || info == nil || info.PID != pid || info.Name == "" || info.IconData == "" {
			t.Fatalf("受限或不存在的进程 %d = (%+v, %v)", pid, info, err)
		}
	}
}

// TestExecutablePathShortName：从 8.3 短路径启动的进程，可执行文件路径报的是长名，按程序名、路径、所在
// 目录写的名单都选得中它。
func TestExecutablePathShortName(t *testing.T) {
	long := copyTestBinary(t, t.TempDir(), "sniffy-short-name-client")
	short := shortPathName(t, long)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	child, server := startOwnerClient(t, listener, short)

	pid := uint32(child.Process.Pid)
	if got, err := ExecutablePath(pid); err != nil || !strings.EqualFold(got, long) {
		t.Fatalf("从 %s 启动: ExecutablePath = (%q, %v)，期望 %q", short, got, err, long)
	}
	finder, err := NewOwnerFinder()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"sniffy-short-name-client", long, filepath.Dir(long)} {
		if got, ok, err := finder.FindOwner(server.RemoteAddr(), server.LocalAddr(), NewAppSet([]string{entry})); err != nil || !ok || got != pid {
			t.Errorf("名单 %q: FindOwner = (%d, %v, %v)，期望属于子进程 %d", entry, got, ok, err, pid)
		}
	}
}

// shortPathName 返回 path 的 8.3 短名写法，卷上没有生成短名时跳过测试。
func shortPathName(t *testing.T, path string) string {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatal(err)
	}
	short := windows.UTF16ToString(buf[:n])
	if !strings.Contains(filepath.Base(short), "~") {
		t.Skipf("%s 所在的卷没有生成 8.3 短名", path)
	}
	return short
}

// sameFile 报告两个路径是否指向同一个文件。TEMP 是 8.3 短名写法时（如 GitHub 的 Windows runner），
// os.Executable 与临时目录下的路径带着短名，进程路径、快捷方式里记的却是长名。
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

func TestParseWindowsTCPTable(t *testing.T) {
	tests := []struct {
		name   string
		family uint32
		hex    string
		want   windowsTCPConnection
	}{
		{
			name: "IPv4含非建立连接", family: windows.AF_INET,
			hex: `02000000
				05000000 7f000001 c7380000 c0000201 01bb0000 78563412
				08000000 7f000001 c7390000 c0000201 01bb0000 2a000000`,
			want: windowsTCPConnection{local: netip.MustParseAddrPort("127.0.0.1:51000"), remote: netip.MustParseAddrPort("192.0.2.1:443"), pid: 0x12345678},
		},
		{
			name: "IPv6含scope", family: windows.AF_INET6,
			hex: `01000000
				fe800000000000000000000000000001 0c000000 c7380000
				fe800000000000000000000000000002 0d000000 01bb0000
				05000000 2a000000`,
			want: windowsTCPConnection{local: netip.MustParseAddrPort("[fe80::1%12]:51000"), remote: netip.MustParseAddrPort("[fe80::2%13]:443"), pid: 42},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer, err := hex.DecodeString(strings.Join(strings.Fields(tt.hex), ""))
			if err != nil {
				t.Fatal(err)
			}
			got, err := parseWindowsTCPTable(buffer, tt.family, false)
			if err != nil || len(got) != 1 || got[0] != tt.want {
				t.Fatalf("连接表 = (%+v, %v)，期望 %+v", got, err, tt.want)
			}
			for length := 0; length < len(buffer); length++ {
				if _, err := parseWindowsTCPTable(buffer[:length], tt.family, false); err == nil {
					t.Fatalf("截断到 %d 字节时应返回错误", length)
				}
			}
		})
	}
	if got, err := parseWindowsTCPTable([]byte{0, 0, 0, 0}, windows.AF_INET, false); err != nil || len(got) != 0 {
		t.Fatalf("空连接表 = (%v, %v)", got, err)
	}
	if _, err := parseWindowsTCPTable([]byte{255, 255, 255, 255}, windows.AF_INET, false); err == nil {
		t.Fatal("超出缓冲区的行数应返回错误")
	}
	if _, err := parseWindowsTCPTable([]byte{0, 0, 0, 0}, 0, false); err == nil {
		t.Fatal("未知地址族应返回错误")
	}
}

// TestParseWindowsTCPTableClosing：要求连同关闭过程中的连接时，返回 FIN_WAIT_1 到 LAST_ACK 的行，
// 不返回监听、握手中与 TIME_WAIT 的。
func TestParseWindowsTCPTableClosing(t *testing.T) {
	var rows []string
	for state := uint32(1); state <= 12; state++ {
		rows = append(rows, fmt.Sprintf("%02x000000 7f000001 c7%02x0000 c0000201 01bb0000 2a000000", state, state))
	}
	buffer, err := hex.DecodeString(strings.Join(strings.Fields(fmt.Sprintf("%02x000000 %s", len(rows), strings.Join(rows, " "))), ""))
	if err != nil {
		t.Fatal(err)
	}
	for closing, want := range map[bool][]uint16{false: {0xc705}, true: {0xc705, 0xc706, 0xc707, 0xc708, 0xc709, 0xc70a}} {
		got, err := parseWindowsTCPTable(buffer, windows.AF_INET, closing)
		if err != nil {
			t.Fatal(err)
		}
		ports := make([]uint16, len(got))
		for i, row := range got {
			ports[i] = row.local.Port()
		}
		if !slices.Equal(ports, want) {
			t.Errorf("closing=%v: 本地端口 %x，期望 %x", closing, ports, want)
		}
	}
}

func FuzzWindowsTCPTable(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0}, false)
	f.Add([]byte{255, 255, 255, 255}, true)
	f.Fuzz(func(t *testing.T, buffer []byte, ipv6 bool) {
		family := uint32(windows.AF_INET)
		if ipv6 {
			family = windows.AF_INET6
		}
		rows, err := parseWindowsTCPTable(buffer, family, true)
		if err != nil {
			return
		}
		for _, row := range rows {
			if !row.local.IsValid() || !row.remote.IsValid() {
				t.Fatalf("连接地址无效: %+v", row)
			}
		}
	})
}

func TestWindowsDetectorLifecycle(t *testing.T) {
	d, err := NewWindowsDetector()
	if err != nil {
		t.Fatal(err)
	}
	if d.iconExtractor == nil || d.isRunning {
		t.Fatalf("检测器初始状态 = %+v", d)
	}
	for range 2 {
		if err := d.Start(); err != nil || !d.isRunning {
			t.Fatalf("启动检测器: %v", err)
		}
	}
	for range 2 {
		if err := d.Stop(); err != nil || d.isRunning {
			t.Fatalf("停止检测器: %v", err)
		}
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { _ = d.Start() })
		wg.Go(func() { _ = d.Stop() })
	}
	wg.Wait()
}
