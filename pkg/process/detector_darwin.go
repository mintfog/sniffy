// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// DarwinDetector macOS系统的进程检测器
type DarwinDetector struct {
	mu          sync.RWMutex
	connections map[string]*ConnectionProcess
	isRunning   bool
}

// newPlatformDetector 创建平台特定的进程检测器
func newPlatformDetector() (Detector, error) {
	return NewDarwinDetector()
}

// NewDarwinDetector 创建macOS进程检测器
func NewDarwinDetector() (*DarwinDetector, error) {
	return &DarwinDetector{
		connections: make(map[string]*ConnectionProcess),
	}, nil
}

// Start 启动检测器
func (d *DarwinDetector) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isRunning {
		return nil
	}

	d.isRunning = true
	return nil
}

// Stop 停止检测器
func (d *DarwinDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.isRunning = false
	return nil
}

// GetProcessByConnection 根据网络连接获取进程信息。
//
// 快路径:用 `lsof -iTCP:<本地端口>` 仅拉取该端口上的 socket(而非全量连接),
// 再按远端端口匹配。避免对全机所有连接逐个 ps 求进程名(原 getLsofConnections
// 路径在繁忙机器上很慢,可能超时)。
func (d *DarwinDetector) GetProcessByConnection(localAddr, remoteAddr net.Addr) (*ProcessInfo, error) {
	localPort := portOf(localAddr)
	if localPort <= 0 {
		return nil, fmt.Errorf("无效的连接地址")
	}

	cmd := exec.Command("lsof", "-nP", "-sTCP:ESTABLISHED", fmt.Sprintf("-iTCP:%d", localPort))
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行lsof命令失败: %v", err)
	}

	connections, err := d.parseLsofOutput(string(output))
	if err != nil {
		return nil, err
	}
	for _, conn := range connections {
		if d.matchConnection(conn, localAddr, remoteAddr) {
			return conn.ProcessInfo, nil
		}
	}

	return nil, fmt.Errorf("未找到匹配的进程")
}

// GetProcessByPID 根据PID获取进程信息
func (d *DarwinDetector) GetProcessByPID(pid uint32) (*ProcessInfo, error) {
	// 使用ps命令获取进程信息
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "pid,comm,args")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行ps命令失败: %v", err)
	}

	return d.parsePsOutput(string(output), pid)
}

// GetAllConnections 获取所有网络连接及其关联的进程信息
func (d *DarwinDetector) GetAllConnections() ([]*ConnectionProcess, error) {
	return d.getLsofConnections()
}

// getLsofConnections 使用lsof获取网络连接信息
func (d *DarwinDetector) getLsofConnections() ([]*ConnectionProcess, error) {
	// 使用lsof命令获取网络连接
	// -i: 选择网络连接
	// -P: 不解析端口名称
	// -n: 不解析主机名
	cmd := exec.Command("lsof", "-i", "-P", "-n")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行lsof命令失败: %v", err)
	}

	return d.parseLsofOutput(string(output))
}

// parseLsofOutput 解析lsof输出
func (d *DarwinDetector) parseLsofOutput(output string) ([]*ConnectionProcess, error) {
	var connections []*ConnectionProcess
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || i == 0 { // 跳过空行和标题行
			continue
		}

		conn, err := d.parseLsofLine(line)
		if err != nil {
			continue // 跳过解析失败的行
		}

		if conn != nil {
			connections = append(connections, conn)
		}
	}

	return connections, nil
}

// parseLsofLine 解析单行lsof输出
func (d *DarwinDetector) parseLsofLine(line string) (*ConnectionProcess, error) {
	// lsof输出格式示例:
	// COMMAND     PID   USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME
	// Chrome    12345   user   42u  IPv4 0x1234567890abcdef      0t0  TCP 127.0.0.1:54321->192.168.1.1:443 (ESTABLISHED)

	fields := strings.Fields(line)
	if len(fields) < 9 {
		return nil, fmt.Errorf("字段数量不足")
	}

	command := fields[0]
	pidStr := fields[1]
	user := fields[2]
	protocol := fields[4]
	name := fields[8]

	// 只处理IPv4和IPv6的TCP/UDP连接
	if !strings.HasPrefix(protocol, "IPv") {
		return nil, nil
	}

	// 解析PID
	pid, err := strconv.ParseUint(pidStr, 10, 32)
	if err != nil {
		return nil, err
	}

	// 解析网络连接信息
	if !strings.Contains(name, "TCP") && !strings.Contains(name, "UDP") {
		return nil, nil
	}

	// 提取协议类型
	var connProtocol string
	if strings.Contains(name, "TCP") {
		connProtocol = "TCP"
	} else if strings.Contains(name, "UDP") {
		connProtocol = "UDP"
	} else {
		return nil, nil
	}

	// 解析连接地址
	// 格式: TCP 127.0.0.1:8080->192.168.1.1:443 (ESTABLISHED)
	addrRegex := regexp.MustCompile(`(\S+):(\d+)->(\S+):(\d+)`)
	matches := addrRegex.FindStringSubmatch(name)
	if len(matches) != 5 {
		return nil, fmt.Errorf("无法解析地址格式: %s", name)
	}

	localIP := matches[1]
	localPortStr := matches[2]
	remoteIP := matches[3]
	remotePortStr := matches[4]

	localPort, err := strconv.Atoi(localPortStr)
	if err != nil {
		return nil, err
	}

	remotePort, err := strconv.Atoi(remotePortStr)
	if err != nil {
		return nil, err
	}

	// 创建地址对象
	localAddr := &net.TCPAddr{
		IP:   net.ParseIP(localIP),
		Port: localPort,
	}

	remoteAddr := &net.TCPAddr{
		IP:   net.ParseIP(remoteIP),
		Port: remotePort,
	}

	// 获取详细进程信息
	processInfo, err := d.GetProcessByPID(uint32(pid))
	if err != nil {
		// 如果获取详细信息失败，使用基本信息
		processInfo = &ProcessInfo{
			PID:  uint32(pid),
			Name: command,
			User: user,
		}
	}

	return &ConnectionProcess{
		LocalAddr:   localAddr,
		RemoteAddr:  remoteAddr,
		Protocol:    connProtocol,
		ProcessInfo: processInfo,
	}, nil
}

// parsePsOutput 解析ps命令输出
func (d *DarwinDetector) parsePsOutput(output string, pid uint32) (*ProcessInfo, error) {
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || i == 0 { // 跳过空行和标题行
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		// 解析ps输出: PID COMMAND ARGS
		parsedPID, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil || uint32(parsedPID) != pid {
			continue
		}

		command := fields[1]
		args := ""
		if len(fields) > 2 {
			args = strings.Join(fields[2:], " ")
		}

		processInfo := &ProcessInfo{
			PID:         pid,
			Name:        command,
			CommandLine: args,
		}

		// 尝试从命令行中提取可执行文件路径
		if args != "" {
			argParts := strings.Fields(args)
			if len(argParts) > 0 {
				processInfo.Path = argParts[0]
			}
		}

		return processInfo, nil
	}

	return nil, fmt.Errorf("未找到PID %d 的进程信息", pid)
}

// matchConnection 匹配网络连接
func (d *DarwinDetector) matchConnection(conn *ConnectionProcess, localAddr, remoteAddr net.Addr) bool {
	if conn.LocalAddr == nil || conn.RemoteAddr == nil {
		return false
	}

	// 比较本地地址
	if localAddr != nil {
		localTCP, ok1 := localAddr.(*net.TCPAddr)
		connLocalTCP, ok2 := conn.LocalAddr.(*net.TCPAddr)
		if ok1 && ok2 {
			if localTCP.Port != connLocalTCP.Port {
				return false
			}
		}
	}

	// 比较远程地址
	if remoteAddr != nil {
		remoteTCP, ok1 := remoteAddr.(*net.TCPAddr)
		connRemoteTCP, ok2 := conn.RemoteAddr.(*net.TCPAddr)
		if ok1 && ok2 {
			if remoteTCP.Port != connRemoteTCP.Port {
				return false
			}
		}
	}

	return true
}
