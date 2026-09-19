// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// LinuxDetector 通过 procfs 查询连接及其所属进程。
type LinuxDetector struct {
	mu        sync.Mutex
	isRunning bool
	procRoot  string
}

func newPlatformDetector() (Detector, error) {
	return NewLinuxDetector()
}

// NewLinuxDetector 创建 Linux 进程检测器。
func NewLinuxDetector() (*LinuxDetector, error) {
	return &LinuxDetector{procRoot: "/proc"}, nil
}

// Start 启动检测器，可重复调用。
func (d *LinuxDetector) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.isRunning = true
	return nil
}

// Stop 停止检测器，可重复调用。
func (d *LinuxDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.isRunning = false
	return nil
}

// GetProcessByConnection 按客户端视角的两端 IP 和端口查询所属进程，包括仍持有 socket 的半关闭连接。
// 传入代理接受的连接时，localAddr 应取 RemoteAddr，remoteAddr 应取 LocalAddr。
func (d *LinuxDetector) GetProcessByConnection(localAddr, remoteAddr net.Addr) (*ProcessInfo, error) {
	local, err := connectionAddrPort(localAddr)
	if err != nil {
		return nil, err
	}
	remote, err := connectionAddrPort(remoteAddr)
	if err != nil {
		return nil, err
	}
	inode, err := d.findConnectionInode(local, remote)
	if err != nil {
		return nil, err
	}
	pid, err := d.findProcessByInode(inode)
	if err != nil {
		return nil, err
	}
	return d.GetProcessByPID(pid)
}

func (d *LinuxDetector) findConnectionInode(local, remote netip.AddrPort) (string, error) {
	// procfs 不携带 IPv6 接口索引，地址比较使用去除 scope 后的 IP。
	localIP, remoteIP := local.Addr().WithZone(""), remote.Addr().WithZone("")
	for _, protocol := range []string{"tcp", "tcp6"} {
		file, err := os.Open(filepath.Join(d.procRoot, "net", protocol))
		if protocol == "tcp6" && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		scanner := bufio.NewScanner(file)
		scanner.Scan()
		var inode string
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			// procfs 数据行的第 2、3、10 个字段依次为本地地址、远端地址、socket inode。
			// 异步查询时连接可能已半关闭，只要 inode 有效就继续查找持有进程。
			if len(fields) < 10 || fields[9] == "0" {
				continue
			}
			if hexPort(fields[1]) != int(local.Port()) || hexPort(fields[2]) != int(remote.Port()) {
				continue
			}
			localAddr, err := parseHexAddr(fields[1])
			if err != nil {
				continue
			}
			remoteAddr, err := parseHexAddr(fields[2])
			if err != nil {
				continue
			}
			localEndpoint, remoteEndpoint := localAddr.AddrPort(), remoteAddr.AddrPort()
			if localEndpoint.Addr().Unmap() == localIP && remoteEndpoint.Addr().Unmap() == remoteIP {
				inode = fields[9]
				break
			}
		}
		err = scanner.Err()
		file.Close()
		if err != nil {
			return "", err
		}
		if inode != "" {
			return inode, nil
		}
	}
	return "", fmt.Errorf("未找到匹配的连接")
}

func hexPort(addr string) int {
	_, value, ok := strings.Cut(addr, ":")
	if !ok {
		return -1
	}
	port, err := strconv.ParseUint(value, 16, 16)
	if err != nil {
		return -1
	}
	return int(port)
}

// GetProcessByPID 在进程目录不存在时报错；字段读取失败时返回已取得的信息。
func (d *LinuxDetector) GetProcessByPID(pid uint32) (*ProcessInfo, error) {
	procDir := filepath.Join(d.procRoot, strconv.FormatUint(uint64(pid), 10))

	if _, err := os.Stat(procDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("进程 %d 不存在", pid)
	}

	processInfo := &ProcessInfo{
		PID: pid,
	}

	commFile := filepath.Join(procDir, "comm")
	if data, err := os.ReadFile(commFile); err == nil {
		processInfo.Name = strings.TrimSpace(string(data))
	}

	exeLink := filepath.Join(procDir, "exe")
	if path, err := os.Readlink(exeLink); err == nil {
		processInfo.Path = path
	}

	cmdlineFile := filepath.Join(procDir, "cmdline")
	if data, err := os.ReadFile(cmdlineFile); err == nil {
		cmdline := string(data)
		cmdline = strings.ReplaceAll(cmdline, "\x00", " ")
		processInfo.CommandLine = strings.TrimSpace(cmdline)
	}

	statusFile := filepath.Join(procDir, "status")
	file, err := os.Open(statusFile)
	if err != nil {
		return processInfo, nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		if uid, err := strconv.Atoi(fields[1]); err == nil {
			processInfo.User = fmt.Sprintf("uid:%d", uid)
		}
		break
	}

	return processInfo, nil
}

// GetAllConnections 返回能关联到持有进程的已建立 TCP 连接。
// 同次结果中，同一 PID 的连接共享 ProcessInfo 指针。
func (d *LinuxDetector) GetAllConnections() ([]*ConnectionProcess, error) {
	var rows []*linuxConnection
	owners := make(map[string]uint32)
	for _, protocol := range []string{"tcp", "tcp6"} {
		file, err := os.Open(filepath.Join(d.procRoot, "net", protocol))
		if protocol == "tcp6" && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		scanner.Scan()
		for scanner.Scan() {
			row, err := parseNetLine(scanner.Text(), protocol)
			if err != nil || row == nil {
				continue
			}
			rows = append(rows, row)
			owners[row.inode] = 0
		}
		err = scanner.Err()
		file.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := d.findSocketOwners(owners); err != nil {
		return nil, err
	}
	connections := make([]*ConnectionProcess, 0, len(rows))
	processes := make(map[uint32]*ProcessInfo)
	for _, row := range rows {
		pid := owners[row.inode]
		if pid == 0 {
			continue
		}
		info, ok := processes[pid]
		if !ok {
			var err error
			info, err = d.GetProcessByPID(pid)
			if err != nil {
				info = &ProcessInfo{PID: pid, Name: fmt.Sprintf("PID_%d", pid)}
			}
			processes[pid] = info
		}
		row.ProcessInfo = info
		connections = append(connections, &row.ConnectionProcess)
	}
	return connections, nil
}

type linuxConnection struct {
	ConnectionProcess
	inode string
}

// parseNetLine 解析 procfs 连接表的数据行；非已建立连接或 inode 为零时返回 (nil, nil)。
func parseNetLine(line, protocol string) (*linuxConnection, error) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return nil, fmt.Errorf("连接表字段不足")
	}
	if fields[3] != "01" || fields[9] == "0" {
		return nil, nil
	}
	local, err := parseHexAddr(fields[1])
	if err != nil {
		return nil, err
	}
	remote, err := parseHexAddr(fields[2])
	if err != nil {
		return nil, err
	}
	return &linuxConnection{
		ConnectionProcess: ConnectionProcess{
			LocalAddr:  local,
			RemoteAddr: remote,
			Protocol:   strings.ToUpper(protocol),
		},
		inode: fields[9],
	}, nil
}

func parseHexAddr(value string) (*net.TCPAddr, error) {
	ipText, portText, ok := strings.Cut(value, ":")
	if !ok || (len(ipText) != 8 && len(ipText) != 32) {
		return nil, fmt.Errorf("无效的 procfs 地址: %q", value)
	}
	ip, err := hex.DecodeString(ipText)
	if err != nil {
		return nil, err
	}
	// procfs 按主机字节序输出每个 32 位地址字，IPv6 同样逐字转换。
	for i := 0; i < len(ip); i += 4 {
		word := binary.BigEndian.Uint32(ip[i : i+4])
		binary.NativeEndian.PutUint32(ip[i:i+4], word)
	}
	port, err := strconv.ParseUint(portText, 16, 16)
	if err != nil {
		return nil, err
	}
	return &net.TCPAddr{IP: net.IP(ip), Port: int(port)}, nil
}

func (d *LinuxDetector) findProcessByInode(inode string) (uint32, error) {
	owners := map[string]uint32{inode: 0}
	if err := d.findSocketOwners(owners); err != nil {
		return 0, err
	}
	if pid := owners[inode]; pid != 0 {
		return pid, nil
	}
	return 0, fmt.Errorf("未找到 socket inode %s 对应的进程", inode)
}

// findSocketOwners 原地填充 owners，调用时所有值必须为零；未找到的项保持为零。
// socket 可被多个进程共享，每项只记录首个可访问的持有进程。
func (d *LinuxDetector) findSocketOwners(owners map[string]uint32) error {
	remaining := len(owners)
	if remaining == 0 {
		return nil
	}
	entries, err := os.ReadDir(d.procRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		pid, err := strconv.ParseUint(entry.Name(), 10, 32)
		if err != nil || pid == 0 || !entry.IsDir() {
			continue
		}
		fdDir := filepath.Join(d.procRoot, entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := strings.CutPrefix(link, "socket:[")
			if !ok {
				continue
			}
			inode, ok = strings.CutSuffix(inode, "]")
			if !ok {
				continue
			}
			if owner, wanted := owners[inode]; !wanted || owner != 0 {
				continue
			}
			owners[inode] = uint32(pid)
			remaining--
			if remaining == 0 {
				return nil
			}
		}
	}
	return nil
}
