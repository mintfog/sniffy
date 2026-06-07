// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// LinuxDetector Linux系统的进程检测器
type LinuxDetector struct {
	mu          sync.RWMutex
	connections map[string]*ConnectionProcess
	isRunning   bool
}

// newPlatformDetector 创建平台特定的进程检测器
func newPlatformDetector() (Detector, error) {
	return NewLinuxDetector()
}

// NewLinuxDetector 创建Linux进程检测器
func NewLinuxDetector() (*LinuxDetector, error) {
	return &LinuxDetector{
		connections: make(map[string]*ConnectionProcess),
	}, nil
}

// Start 启动检测器
func (d *LinuxDetector) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isRunning {
		return nil
	}

	d.isRunning = true
	return nil
}

// Stop 停止检测器
func (d *LinuxDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.isRunning = false
	return nil
}

// GetProcessByConnection 根据网络连接获取进程信息。
//
// 快路径:先按 (本地端口, 远端端口) 在 /proc/net/tcp{,6} 中定位目标 socket 的 inode,
// 再仅对该 inode 做一次 /proc/*/fd 扫描求 PID。避免对每条连接都扫一遍 /proc(原
// GetAllConnections 路径在繁忙机器上是 O(连接数 × 进程数),会拖到数秒。
func (d *LinuxDetector) GetProcessByConnection(localAddr, remoteAddr net.Addr) (*ProcessInfo, error) {
	localPort := portOf(localAddr)
	remotePort := portOf(remoteAddr)
	if localPort <= 0 || remotePort <= 0 {
		return nil, fmt.Errorf("无效的连接地址")
	}

	inode := d.findInodeByPorts(localPort, remotePort)
	if inode == "" {
		return nil, fmt.Errorf("未找到匹配的连接")
	}

	pid, err := d.findProcessByInode(inode)
	if err != nil {
		return nil, err
	}
	return d.GetProcessByPID(pid)
}

// findInodeByPorts 在 /proc/net/tcp 与 tcp6 中查找本地/远端端口匹配的 socket inode。
func (d *LinuxDetector) findInodeByPorts(localPort, remotePort int) string {
	for _, proto := range []string{"tcp", "tcp6"} {
		file, err := os.Open(fmt.Sprintf("/proc/net/%s", proto))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Scan() // 跳过标题行
		for scanner.Scan() {
			fields := strings.Fields(strings.TrimSpace(scanner.Text()))
			if len(fields) < 10 {
				continue
			}
			if hexPort(fields[1]) == localPort && hexPort(fields[2]) == remotePort {
				file.Close()
				return fields[9]
			}
		}
		file.Close()
	}
	return ""
}

// hexPort 从 "IPHEX:PORTHEX" 的地址字段中解析端口(十六进制)。
func hexPort(addr string) int {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 || i+1 >= len(addr) {
		return -1
	}
	p, err := strconv.ParseInt(addr[i+1:], 16, 32)
	if err != nil {
		return -1
	}
	return int(p)
}

// GetProcessByPID 根据PID获取进程信息
func (d *LinuxDetector) GetProcessByPID(pid uint32) (*ProcessInfo, error) {
	procDir := fmt.Sprintf("/proc/%d", pid)

	// 检查进程是否存在
	if _, err := os.Stat(procDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("进程 %d 不存在", pid)
	}

	processInfo := &ProcessInfo{
		PID: pid,
	}

	// 读取进程名称
	commFile := filepath.Join(procDir, "comm")
	if data, err := os.ReadFile(commFile); err == nil {
		processInfo.Name = strings.TrimSpace(string(data))
	}

	// 读取可执行文件路径
	exeLink := filepath.Join(procDir, "exe")
	if path, err := os.Readlink(exeLink); err == nil {
		processInfo.Path = path
	}

	// 读取命令行参数
	cmdlineFile := filepath.Join(procDir, "cmdline")
	if data, err := os.ReadFile(cmdlineFile); err == nil {
		// cmdline文件中参数以null字符分隔
		cmdline := string(data)
		cmdline = strings.ReplaceAll(cmdline, "\x00", " ")
		processInfo.CommandLine = strings.TrimSpace(cmdline)
	}

	// 读取进程所有者
	statFile := filepath.Join(procDir, "status")
	if file, err := os.Open(statFile); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if uid, err := strconv.Atoi(fields[1]); err == nil {
						processInfo.User = fmt.Sprintf("uid:%d", uid)
					}
				}
				break
			}
		}
	}

	return processInfo, nil
}

// GetAllConnections 获取所有网络连接及其关联的进程信息
func (d *LinuxDetector) GetAllConnections() ([]*ConnectionProcess, error) {
	var connections []*ConnectionProcess

	// 读取TCP连接
	tcpConns, err := d.parseProcNet("tcp")
	if err != nil {
		return nil, err
	}
	connections = append(connections, tcpConns...)

	// 读取TCP6连接
	tcp6Conns, err := d.parseProcNet("tcp6")
	if err != nil {
		return nil, err
	}
	connections = append(connections, tcp6Conns...)

	return connections, nil
}

// parseProcNet 解析/proc/net/tcp或/proc/net/tcp6文件
func (d *LinuxDetector) parseProcNet(protocol string) ([]*ConnectionProcess, error) {
	var connections []*ConnectionProcess

	file, err := os.Open(fmt.Sprintf("/proc/net/%s", protocol))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// 跳过标题行
	scanner.Scan()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		conn, err := d.parseNetLine(line, protocol)
		if err != nil {
			continue // 跳过解析失败的行
		}

		if conn != nil {
			connections = append(connections, conn)
		}
	}

	return connections, nil
}

// parseNetLine 解析单行/proc/net/tcp数据
func (d *LinuxDetector) parseNetLine(line, protocol string) (*ConnectionProcess, error) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return nil, fmt.Errorf("字段数量不足")
	}

	// 字段格式: sl local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode
	localAddrStr := fields[1]
	remoteAddrStr := fields[2]
	state := fields[3]
	inode := fields[9]

	// 只处理ESTABLISHED状态的连接(state == 01)
	if protocol == "tcp" && state != "01" {
		return nil, nil
	}

	// 解析地址
	localAddr, err := d.parseHexAddr(localAddrStr)
	if err != nil {
		return nil, err
	}

	remoteAddr, err := d.parseHexAddr(remoteAddrStr)
	if err != nil {
		return nil, err
	}

	// 通过inode查找对应的进程
	pid, err := d.findProcessByInode(inode)
	if err != nil {
		return nil, err
	}

	// 获取进程信息
	processInfo, err := d.GetProcessByPID(pid)
	if err != nil {
		processInfo = &ProcessInfo{
			PID:  pid,
			Name: fmt.Sprintf("PID_%d", pid),
		}
	}

	return &ConnectionProcess{
		LocalAddr:   localAddr,
		RemoteAddr:  remoteAddr,
		Protocol:    strings.ToUpper(protocol),
		ProcessInfo: processInfo,
	}, nil
}

// parseHexAddr 解析十六进制地址格式
func (d *LinuxDetector) parseHexAddr(hexAddr string) (net.Addr, error) {
	parts := strings.Split(hexAddr, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("地址格式错误: %s", hexAddr)
	}

	// 解析IP地址（小端序）
	ipHex := parts[0]
	if len(ipHex) == 8 {
		// IPv4
		ip := make(net.IP, 4)
		for i := 0; i < 4; i++ {
			b, err := strconv.ParseUint(ipHex[6-i*2:8-i*2], 16, 8)
			if err != nil {
				return nil, err
			}
			ip[i] = byte(b)
		}
	} else if len(ipHex) == 32 {
		// IPv6处理更复杂，这里简化处理
		return nil, fmt.Errorf("IPv6地址解析暂未实现")
	}

	// 解析端口
	portHex := parts[1]
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return nil, err
	}

	// 创建IPv4地址
	ip := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		b, err := strconv.ParseUint(ipHex[6-i*2:8-i*2], 16, 8)
		if err != nil {
			return nil, err
		}
		ip[i] = byte(b)
	}

	return &net.TCPAddr{
		IP:   ip,
		Port: int(port),
	}, nil
}

// findProcessByInode 通过inode查找进程PID
func (d *LinuxDetector) findProcessByInode(inode string) (uint32, error) {
	// 遍历/proc目录下的进程
	procDirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		return 0, err
	}

	for _, procDir := range procDirs {
		fdDir := filepath.Join(procDir, "fd")

		// 检查fd目录是否存在和可访问
		if _, err := os.Stat(fdDir); os.IsNotExist(err) {
			continue
		}

		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // 权限不足时跳过
		}

		for _, fd := range fds {
			fdPath := filepath.Join(fdDir, fd.Name())
			if link, err := os.Readlink(fdPath); err == nil {
				if strings.Contains(link, fmt.Sprintf("[%s]", inode)) {
					// 提取PID
					pidStr := filepath.Base(procDir)
					if pid, err := strconv.ParseUint(pidStr, 10, 32); err == nil {
						return uint32(pid), nil
					}
				}
			}
		}
	}

	return 0, fmt.Errorf("未找到inode %s 对应的进程", inode)
}
