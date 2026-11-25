// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ownerFinder struct{}

func newOwnerFinder() (ownerFinder, error) {
	return ownerFinder{}, getExtendedTCPTable.Find()
}

// find 从连接表取持有者的 PID，再现查它的可执行文件：PID 复用得快，按 PID 缓存的结果靠不住。应用建连后
// 马上关了写端（等服务端先开口的程序）的连接也算它持有，与 Linux、macOS 一致。
func (f *ownerFinder) find(local, remote netip.AddrPort, apps *AppSet) (uint32, bool, error) {
	family := uint32(windows.AF_INET)
	if local.Addr().Is6() {
		family = windows.AF_INET6
	}
	connections, err := readWindowsTCPTable(family, true)
	if err != nil {
		return 0, false, err
	}
	for _, conn := range connections {
		if conn.local != local || conn.remote != remote {
			continue
		}
		path, err := ExecutablePath(conn.pid)
		name := ""
		if err != nil {
			// Toolhelp 能读到部分受保护进程的名字，它们的路径查询会被拒绝。
			name = windowsProcessName(conn.pid)
		}
		return conn.pid, apps.Match(path, name), nil
	}
	return 0, false, errors.New("未找到匹配的连接")
}

// runningImages 经 Toolhelp 列出全部进程。受保护的进程查不到路径，只按 Toolhelp 给出的文件名比较。
func runningImages() ([]runningImage, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("列出进程: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	var images []runningImage
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		path, _ := ExecutablePath(entry.ProcessID)
		images = append(images, runningImage{path: path, name: windows.UTF16ToString(entry.ExeFile[:])})
	}
	return images, nil
}
