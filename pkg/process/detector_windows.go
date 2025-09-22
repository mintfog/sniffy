// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WindowsDetector Windows系统的进程检测器
type WindowsDetector struct {
	mu          sync.RWMutex
	connections map[string]*ConnectionProcess
	isRunning   bool
}

// newPlatformDetector 创建平台特定的进程检测器
func newPlatformDetector() (Detector, error) {
	return NewWindowsDetector()
}

// NewWindowsDetector 创建Windows进程检测器
func NewWindowsDetector() (*WindowsDetector, error) {
	return &WindowsDetector{
		connections: make(map[string]*ConnectionProcess),
	}, nil
}

// Start 启动检测器
func (d *WindowsDetector) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isRunning {
		return nil
	}

	d.isRunning = true
	return nil
}

// Stop 停止检测器
func (d *WindowsDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.isRunning = false
	return nil
}

// GetProcessByConnection 根据网络连接获取进程信息
func (d *WindowsDetector) GetProcessByConnection(localAddr, remoteAddr net.Addr) (*ProcessInfo, error) {
	connections, err := d.getNetstatConnections()
	if err != nil {
		return nil, err
	}

	// 尝试匹配连接
	for _, conn := range connections {
		if d.matchConnection(conn, localAddr, remoteAddr) {
			return conn.ProcessInfo, nil
		}
	}

	return nil, fmt.Errorf("未找到匹配的进程")
}

// GetProcessByPID 根据PID获取进程信息
func (d *WindowsDetector) GetProcessByPID(pid uint32) (*ProcessInfo, error) {
	// 使用超时防止卡住
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 使用tasklist的简单版本快速获取进程名
	cmd := exec.CommandContext(ctx, "tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")

	output, err := cmd.Output()
	if err != nil {
		// 如果失败，返回基本进程信息
		return &ProcessInfo{
			PID:  pid,
			Name: fmt.Sprintf("PID_%d", pid),
		}, nil
	}

	return d.parseTasklistSimple(string(output), pid)
}

// GetAllConnections 获取所有网络连接及其关联的进程信息
func (d *WindowsDetector) GetAllConnections() ([]*ConnectionProcess, error) {
	return d.getNetstatConnections()
}

// getNetstatConnections 使用netstat获取网络连接信息
func (d *WindowsDetector) getNetstatConnections() ([]*ConnectionProcess, error) {
	// 使用超时防止程序卡住
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "netstat", "-ano")
	output, err := cmd.Output()
	if err != nil {
		// 如果netstat失败，返回空连接列表而不是尝试PowerShell
		return []*ConnectionProcess{}, nil
	}

	return d.parseNetstatOutput(string(output))
}

// parseNetstatOutput 解析netstat输出
func (d *WindowsDetector) parseNetstatOutput(output string) ([]*ConnectionProcess, error) {
	var connections []*ConnectionProcess
	lines := strings.Split(output, "\n")

	// 限制处理的连接数量避免卡顿
	processedCount := 0
	maxConnections := 20

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 只处理包含ESTABLISHED的TCP连接
		if !strings.Contains(line, "TCP") || !strings.Contains(line, "ESTABLISHED") {
			continue
		}

		// 简单的字段分割方式
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		protocol := fields[0]
		localAddrStr := fields[1]
		remoteAddrStr := fields[2]
		state := fields[3]
		pidStr := fields[4]

		// 验证状态
		if state != "ESTABLISHED" {
			continue
		}

		pid, err := strconv.ParseUint(pidStr, 10, 32)
		if err != nil {
			continue
		}

		// 简单地址解析
		localAddr, err := d.parseAddressSimple(localAddrStr)
		if err != nil {
			continue
		}

		remoteAddr, err := d.parseAddressSimple(remoteAddrStr)
		if err != nil {
			continue
		}

		// 尝试快速获取进程名称
		processInfo, err := d.GetProcessByPID(uint32(pid))
		if err != nil {
			// 如果获取失败，使用基本信息
			processInfo = &ProcessInfo{
				PID:  uint32(pid),
				Name: fmt.Sprintf("PID_%d", pid),
				Path: "",
			}
		}

		conn := &ConnectionProcess{
			LocalAddr:   localAddr,
			RemoteAddr:  remoteAddr,
			Protocol:    protocol,
			ProcessInfo: processInfo,
		}

		connections = append(connections, conn)

		processedCount++
		if processedCount >= maxConnections {
			break
		}
	}
	return connections, nil
}

// parseWmicProcessOutput 解析wmic进程输出
func (d *WindowsDetector) parseWmicProcessOutput(output string, pid uint32) (*ProcessInfo, error) {
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "CommandLine,ExecutablePath,Name,ProcessId") {
			continue
		}

		// CSV格式解析
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}

		processInfo := &ProcessInfo{
			PID:         pid,
			CommandLine: strings.TrimSpace(fields[0]),
			Path:        strings.TrimSpace(fields[1]),
			Name:        strings.TrimSpace(fields[2]),
		}

		// 如果没有获取到进程名称，尝试从路径中提取
		if processInfo.Name == "" && processInfo.Path != "" {
			parts := strings.Split(processInfo.Path, "\\")
			if len(parts) > 0 {
				processInfo.Name = parts[len(parts)-1]
			}
		}

		return processInfo, nil
	}

	return nil, fmt.Errorf("未找到PID %d 的进程信息", pid)
}

// parseAddress 解析地址字符串
func (d *WindowsDetector) parseAddress(addrStr string) (net.Addr, error) {
	if addrStr == "*:*" || addrStr == "0.0.0.0:0" {
		return &net.TCPAddr{IP: net.IPv4zero, Port: 0}, nil
	}

	// 处理IPv6地址格式 [::1]:8080
	if strings.HasPrefix(addrStr, "[") {
		parts := strings.Split(addrStr, "]:")
		if len(parts) == 2 {
			ip := net.ParseIP(strings.TrimPrefix(parts[0], "["))
			port, err := strconv.Atoi(parts[1])
			if err != nil {
				return nil, err
			}
			return &net.TCPAddr{IP: ip, Port: port}, nil
		}
	}

	// 处理IPv4地址格式 127.0.0.1:8080
	parts := strings.Split(addrStr, ":")
	if len(parts) == 2 {
		ip := net.ParseIP(parts[0])
		port, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}
		return &net.TCPAddr{IP: ip, Port: port}, nil
	}

	return nil, fmt.Errorf("无法解析地址: %s", addrStr)
}

// matchConnection 匹配网络连接
func (d *WindowsDetector) matchConnection(conn *ConnectionProcess, localAddr, remoteAddr net.Addr) bool {
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
			// 可以添加IP匹配逻辑
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
			// 可以添加IP匹配逻辑
		}
	}

	return true
}

// parseAddressSimple 简单地址解析方法，避免复杂解析导致卡顿
func (d *WindowsDetector) parseAddressSimple(addrStr string) (net.Addr, error) {
	if addrStr == "*:*" || addrStr == "0.0.0.0:0" {
		return &net.TCPAddr{IP: net.IPv4zero, Port: 0}, nil
	}

	// 处理IPv4地址格式 127.0.0.1:8080
	parts := strings.Split(addrStr, ":")
	if len(parts) == 2 {
		ip := net.ParseIP(parts[0])
		port, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}
		return &net.TCPAddr{IP: ip, Port: port}, nil
	}

	return nil, fmt.Errorf("无法解析地址: %s", addrStr)
}

// parseTasklistSimple 简化版tasklist输出解析
func (d *WindowsDetector) parseTasklistSimple(output string, pid uint32) (*ProcessInfo, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 去除引号并分割CSV字段
		line = strings.ReplaceAll(line, `"`, "")
		fields := strings.Split(line, ",")

		if len(fields) >= 2 {
			processName := strings.TrimSpace(fields[0])
			return &ProcessInfo{
				PID:  pid,
				Name: processName,
			}, nil
		}
	}

	return &ProcessInfo{
		PID:  pid,
		Name: fmt.Sprintf("PID_%d", pid),
	}, nil
}

// getProcessByPowerShell 使用PowerShell获取进程信息
func (d *WindowsDetector) getProcessByPowerShell(pid uint32) (*ProcessInfo, error) {
	// 使用PowerShell Get-Process命令
	script := fmt.Sprintf(`Get-Process -Id %d | Select-Object Id,Name,Path,@{Name="CommandLine";Expression={$_.StartInfo.Arguments}} | ConvertTo-Json`, pid)
	cmd := exec.Command("powershell", "-Command", script)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行PowerShell命令失败: %v", err)
	}

	return d.parsePowerShellOutput(string(output), pid)
}

// getProcessByTasklist 使用tasklist获取进程信息
func (d *WindowsDetector) getProcessByTasklist(pid uint32) (*ProcessInfo, error) {
	// 使用tasklist /FI命令
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/V")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行tasklist命令失败: %v", err)
	}

	return d.parseTasklistOutput(string(output), pid)
}

// getConnectionsByPowerShell 使用PowerShell获取网络连接
func (d *WindowsDetector) getConnectionsByPowerShell() ([]*ConnectionProcess, error) {
	// 使用PowerShell Get-NetTCPConnection命令
	script := `Get-NetTCPConnection | Where-Object {$_.State -eq "Established"} | Select-Object LocalAddress,LocalPort,RemoteAddress,RemotePort,OwningProcess | ConvertTo-Json`
	cmd := exec.Command("powershell", "-Command", script)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行PowerShell网络命令失败: %v", err)
	}

	return d.parsePowerShellNetOutput(string(output))
}

// parsePowerShellOutput 解析PowerShell进程输出
func (d *WindowsDetector) parsePowerShellOutput(output string, pid uint32) (*ProcessInfo, error) {
	// 简单的JSON解析（实际应使用json包）
	lines := strings.Split(output, "\n")
	processInfo := &ProcessInfo{
		PID: pid,
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, `"Name"`) {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				name := strings.Trim(strings.TrimSpace(parts[1]), `",`)
				processInfo.Name = name
			}
		} else if strings.Contains(line, `"Path"`) {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				path := strings.Trim(strings.TrimSpace(parts[1]), `",`)
				processInfo.Path = path
			}
		}
	}

	return processInfo, nil
}

// parseTasklistOutput 解析tasklist输出
func (d *WindowsDetector) parseTasklistOutput(output string, pid uint32) (*ProcessInfo, error) {
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		if i == 0 { // 跳过标题行
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// CSV格式解析
		fields := strings.Split(line, ",")
		if len(fields) < 8 {
			continue
		}

		// 去除引号
		for j := range fields {
			fields[j] = strings.Trim(fields[j], `"`)
		}

		// 检查PID是否匹配
		if pidStr := fields[1]; pidStr != "" {
			if parsedPID, err := strconv.ParseUint(pidStr, 10, 32); err == nil && uint32(parsedPID) == pid {
				return &ProcessInfo{
					PID:  pid,
					Name: fields[0],
					User: fields[6], // 用户名在第6列
					// tasklist不提供完整路径信息
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("在tasklist输出中未找到PID %d", pid)
}

// parsePowerShellNetOutput 解析PowerShell网络连接输出
func (d *WindowsDetector) parsePowerShellNetOutput(output string) ([]*ConnectionProcess, error) {
	var connections []*ConnectionProcess

	// 简单的解析实现（实际应使用json包）
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "LocalAddress") {
			continue
		}

		// 提取基本信息（这里简化了处理）
		// 实际应该使用proper JSON解析
		conn := &ConnectionProcess{
			Protocol: "TCP",
		}

		connections = append(connections, conn)
	}

	return connections, nil
}
