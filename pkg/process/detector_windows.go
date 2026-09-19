// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WindowsDetector 通过 Windows API 查询 TCP 连接及其所属进程。
type WindowsDetector struct {
	mu            sync.Mutex
	isRunning     bool
	iconExtractor *IconExtractor
}

func newPlatformDetector() (Detector, error) {
	return NewWindowsDetector()
}

// NewWindowsDetector 创建 Windows 进程检测器。
func NewWindowsDetector() (*WindowsDetector, error) {
	return &WindowsDetector{iconExtractor: NewIconExtractor()}, nil
}

// Start 启动检测器，可重复调用。
func (d *WindowsDetector) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.isRunning = true
	return nil
}

// Stop 停止检测器，可重复调用。
func (d *WindowsDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.isRunning = false
	return nil
}

// GetProcessByConnection 按客户端视角的两端地址查询已建立的 TCP 连接。
// 传入代理接受的连接时，localAddr 应取 RemoteAddr，remoteAddr 应取 LocalAddr。
func (d *WindowsDetector) GetProcessByConnection(localAddr, remoteAddr net.Addr) (*ProcessInfo, error) {
	local, err := connectionAddrPort(localAddr)
	if err != nil {
		return nil, err
	}
	remote, err := connectionAddrPort(remoteAddr)
	if err != nil {
		return nil, err
	}
	if local.Addr().Is4() != remote.Addr().Is4() {
		return nil, fmt.Errorf("连接两端的地址族不一致")
	}
	family := uint32(windows.AF_INET)
	if local.Addr().Is6() {
		family = windows.AF_INET6
	}
	connections, err := readWindowsTCPTable(family)
	if err != nil {
		return nil, err
	}
	for _, conn := range connections {
		if conn.local == local && conn.remote == remote {
			return d.GetProcessByPID(conn.pid)
		}
	}
	return nil, fmt.Errorf("未找到匹配的连接")
}

// GetProcessByPID 返回进程信息；进程已退出或访问受限时保留 PID 和可读取的字段。
func (d *WindowsDetector) GetProcessByPID(pid uint32) (*ProcessInfo, error) {
	info := &ProcessInfo{PID: pid, Name: fmt.Sprintf("PID_%d", pid)}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err == nil {
		defer windows.CloseHandle(handle)
		for size := uint32(260); ; size = min(size*2, 32768) {
			buffer := make([]uint16, size)
			length := size
			err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &length)
			if err == nil {
				info.Path = windows.UTF16ToString(buffer[:length])
				info.Name = filepath.Base(info.Path)
				break
			}
			if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size == 32768 {
				break
			}
		}
		info.User = windowsProcessUser(handle)
	}
	if info.Path == "" {
		// Toolhelp 可读取部分受保护进程的名称，路径查询仍可能被系统拒绝。
		if name := windowsProcessName(pid); name != "" {
			info.Name = name
		}
	}
	var icon *ProcessIconInfo
	if info.Path == "" {
		icon = d.iconExtractor.getIconByFileName(info.Name)
	} else {
		icon, _ = d.iconExtractor.ExtractIcon(info.Path)
	}
	if icon != nil {
		info.IconData = icon.IconData
		info.IconType = icon.IconType
		info.IconSize = icon.IconSize
		info.HasIcon = icon.HasIcon
		info.IconCategory = icon.IconCategory
	}
	return info, nil
}

func windowsProcessUser(handle windows.Handle) string {
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return ""
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return ""
	}
	name, domain, _, err := user.User.Sid.LookupAccount("")
	if err != nil {
		return user.User.Sid.String()
	}
	if domain != "" {
		return domain + `\` + name
	}
	return name
}

func windowsProcessName(pid uint32) string {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == pid {
			return windows.UTF16ToString(entry.ExeFile[:])
		}
	}
	return ""
}

// GetAllConnections 返回 IPv4、IPv6 已建立的 TCP 连接。
// 同次结果中，同一 PID 的连接共享 ProcessInfo 指针。
func (d *WindowsDetector) GetAllConnections() ([]*ConnectionProcess, error) {
	connections := make([]*ConnectionProcess, 0)
	processes := make(map[uint32]*ProcessInfo)
	for _, family := range []uint32{windows.AF_INET, windows.AF_INET6} {
		rows, err := readWindowsTCPTable(family)
		if errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			info, ok := processes[row.pid]
			if !ok {
				info, _ = d.GetProcessByPID(row.pid)
				processes[row.pid] = info
			}
			connections = append(connections, &ConnectionProcess{
				LocalAddr:   net.TCPAddrFromAddrPort(row.local),
				RemoteAddr:  net.TCPAddrFromAddrPort(row.remote),
				Protocol:    "TCP",
				ProcessInfo: info,
			})
		}
	}
	return connections, nil
}
