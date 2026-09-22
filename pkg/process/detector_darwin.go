// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// DarwinDetector 通过 libproc 识别本机 TCP 连接的所属进程。
type DarwinDetector struct {
	api *darwinLibproc

	mu             sync.Mutex
	scanStartedAt  time.Time
	scanFinishedAt time.Time
	connections    map[darwinConnectionKey]darwinConnection
}

func newPlatformDetector() (Detector, error) {
	return NewDarwinDetector()
}

// NewDarwinDetector 加载原生查询接口，不要求启用 CGO。
func NewDarwinDetector() (*DarwinDetector, error) {
	api, err := loadDarwinLibproc()
	if err != nil {
		return nil, err
	}
	return &DarwinDetector{api: api}, nil
}

// Start 无需启动后台任务，连接在查询时按需扫描。
func (d *DarwinDetector) Start() error {
	return nil
}

// Stop 释放连接快照，可重复调用。
func (d *DarwinDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.connections = nil
	d.scanStartedAt = time.Time{}
	d.scanFinishedAt = time.Time{}
	return nil
}

// GetProcessByConnection 按客户端视角的两端 IP 和端口查找所属进程。
// 传入代理接受的连接时，localAddr 取 RemoteAddr，remoteAddr 取 LocalAddr。
func (d *DarwinDetector) GetProcessByConnection(localAddr, remoteAddr net.Addr) (*ProcessInfo, error) {
	local, err := connectionAddrPort(localAddr)
	if err != nil {
		return nil, err
	}
	remote, err := connectionAddrPort(remoteAddr)
	if err != nil {
		return nil, err
	}

	conn, err := d.findConnection(darwinConnectionKey{local: local, remote: remote})
	if err != nil {
		return nil, err
	}
	return d.GetProcessByPID(conn.pid)
}

func (d *DarwinDetector) findConnection(key darwinConnectionKey) (darwinConnection, error) {
	lookupStartedAt := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	if time.Since(d.scanFinishedAt) < 100*time.Millisecond {
		if conn, ok := d.connections[key]; ok && d.api.ownsConnection(conn) {
			return conn, nil
		}
		// 复用等待锁期间开始的扫描；较早快照中的缺失不能证明新连接不存在。
		if d.scanStartedAt.After(lookupStartedAt) {
			return darwinConnection{}, fmt.Errorf("未找到匹配的进程")
		}
	}

	scanStartedAt := time.Now()
	connections, err := d.api.connections()
	if err != nil {
		return darwinConnection{}, err
	}
	d.connections = make(map[darwinConnectionKey]darwinConnection, len(connections))
	for _, conn := range connections {
		d.connections[conn.darwinConnectionKey] = conn
	}
	d.scanStartedAt = scanStartedAt
	d.scanFinishedAt = time.Now()

	if conn, ok := d.connections[key]; ok && d.api.ownsConnection(conn) {
		return conn, nil
	}
	return darwinConnection{}, fmt.Errorf("未找到匹配的进程")
}

// GetProcessByPID 返回名称、可执行文件路径及 UID；无权读取的可选字段保持为空。
func (d *DarwinDetector) GetProcessByPID(pid uint32) (*ProcessInfo, error) {
	if pid == 0 || pid > 1<<31-1 {
		return nil, fmt.Errorf("无效的 PID: %d", pid)
	}

	var bsd [darwinBSDSize]byte
	var path [4096]byte
	bsdOK := d.api.pidInfo(int32(pid), procPIDTBSDInfo, 0, &bsd[0], darwinBSDSize) == darwinBSDSize
	pathSize := d.api.pidPath(int32(pid), &path[0], uint32(len(path)))
	if !bsdOK && pathSize <= 0 {
		return nil, fmt.Errorf("无法读取进程 %d", pid)
	}

	info := &ProcessInfo{PID: pid}
	if bsdOK {
		info.User = fmt.Sprintf("uid:%d", binary.LittleEndian.Uint32(bsd[darwinUIDOffset:]))
		info.Name = darwinCString(bsd[darwinNameOffset : darwinNameOffset+32])
		if info.Name == "" {
			info.Name = darwinCString(bsd[darwinCommOffset : darwinCommOffset+16])
		}
	}
	if pathSize > 0 && int(pathSize) < len(path) {
		info.Path = darwinCString(path[:])
		info.Name = filepath.Base(info.Path)
	}
	if info.Name == "" {
		info.Name = fmt.Sprintf("PID_%d", pid)
	}
	if args, err := unix.SysctlRaw("kern.procargs2", int(pid)); err == nil {
		info.CommandLine = darwinCommandLine(args)
	}
	return info, nil
}

// GetAllConnections 返回当前用户有权读取的已建立 TCP 连接。
func (d *DarwinDetector) GetAllConnections() ([]*ConnectionProcess, error) {
	rows, err := d.api.connections()
	if err != nil {
		return nil, err
	}

	processes := make(map[uint32]*ProcessInfo)
	connections := make([]*ConnectionProcess, 0, len(rows))
	for _, row := range rows {
		if row.state != darwinTCPEstablished {
			continue
		}
		info, ok := processes[row.pid]
		if !ok {
			info, err = d.GetProcessByPID(row.pid)
			if err != nil {
				info = &ProcessInfo{PID: row.pid, Name: fmt.Sprintf("PID_%d", row.pid)}
			}
			processes[row.pid] = info
		}
		connections = append(connections, &ConnectionProcess{
			LocalAddr:   net.TCPAddrFromAddrPort(row.local),
			RemoteAddr:  net.TCPAddrFromAddrPort(row.remote),
			Protocol:    "TCP",
			ProcessInfo: info,
		})
	}
	return connections, nil
}

func darwinCString(value []byte) string {
	if end := bytes.IndexByte(value, 0); end >= 0 {
		value = value[:end]
	}
	return string(value)
}

func darwinCommandLine(buffer []byte) string {
	if len(buffer) < 4 {
		return ""
	}
	argc := int(binary.LittleEndian.Uint32(buffer))
	buffer = buffer[4:]

	// XNU 将路径（含 NUL）按 64 位进程的指针宽度对齐后写入 argv。
	// sysctl 剥离的 executable_path= 前缀占 16 字节，不影响对齐。
	pathEnd := bytes.IndexByte(buffer, 0)
	if pathEnd < 0 {
		return ""
	}
	argvOffset := (pathEnd + 1 + 7) &^ 7
	if argvOffset > len(buffer) {
		return ""
	}
	for _, padding := range buffer[pathEnd+1 : argvOffset] {
		if padding != 0 {
			return ""
		}
	}
	buffer = buffer[argvOffset:]
	if argc <= 0 || argc > len(buffer) {
		return ""
	}

	// 空参数也占用 argc；跳过它会把后面的环境变量读入命令行。
	args := make([]string, 0, argc)
	for range argc {
		end := bytes.IndexByte(buffer, 0)
		if end < 0 {
			return ""
		}
		args = append(args, string(buffer[:end]))
		buffer = buffer[end+1:]
	}
	return strings.Join(args, " ")
}
