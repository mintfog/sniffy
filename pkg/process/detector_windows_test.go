// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
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
	if !strings.EqualFold(info.Path, executable) || info.Name != filepath.Base(info.Path) || info.User == "" || info.IconData == "" {
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
			got, err := parseWindowsTCPTable(buffer, tt.family)
			if err != nil || len(got) != 1 || got[0] != tt.want {
				t.Fatalf("连接表 = (%+v, %v)，期望 %+v", got, err, tt.want)
			}
			for length := 0; length < len(buffer); length++ {
				if _, err := parseWindowsTCPTable(buffer[:length], tt.family); err == nil {
					t.Fatalf("截断到 %d 字节时应返回错误", length)
				}
			}
		})
	}
	if got, err := parseWindowsTCPTable([]byte{0, 0, 0, 0}, windows.AF_INET); err != nil || len(got) != 0 {
		t.Fatalf("空连接表 = (%v, %v)", got, err)
	}
	if _, err := parseWindowsTCPTable([]byte{255, 255, 255, 255}, windows.AF_INET); err == nil {
		t.Fatal("超出缓冲区的行数应返回错误")
	}
	if _, err := parseWindowsTCPTable([]byte{0, 0, 0, 0}, 0); err == nil {
		t.Fatal("未知地址族应返回错误")
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
		rows, err := parseWindowsTCPTable(buffer, family)
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
