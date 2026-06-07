// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"net"
	"strconv"
)

// ProcessInfo 进程信息结构体
type ProcessInfo struct {
	PID         uint32 `json:"pid"`         // 进程ID
	Name        string `json:"name"`        // 进程名称
	Path        string `json:"path"`        // 程序路径
	CommandLine string `json:"commandLine"` // 命令行参数
	User        string `json:"user"`        // 所属用户
	// 图标信息
	IconData     string `json:"iconData"`     // Base64编码的图标数据
	IconType     string `json:"iconType"`     // 图标类型 (ico, png, svg)
	IconSize     string `json:"iconSize"`     // 图标尺寸 (16x16, 32x32, etc.)
	HasIcon      bool   `json:"hasIcon"`      // 是否有图标
	IconCategory string `json:"iconCategory"` // 图标类别 (browser, development, system, etc.)
}

// ConnectionProcess 连接关联的进程信息
type ConnectionProcess struct {
	LocalAddr   net.Addr     `json:"localAddr"`   // 本地地址
	RemoteAddr  net.Addr     `json:"remoteAddr"`  // 远程地址
	Protocol    string       `json:"protocol"`    // 协议类型
	ProcessInfo *ProcessInfo `json:"processInfo"` // 进程信息
}

// Detector 进程检测器接口
type Detector interface {
	// GetProcessByConnection 根据网络连接获取进程信息
	GetProcessByConnection(localAddr, remoteAddr net.Addr) (*ProcessInfo, error)

	// GetProcessByPID 根据PID获取进程信息
	GetProcessByPID(pid uint32) (*ProcessInfo, error)

	// GetAllConnections 获取所有网络连接及其关联的进程信息
	GetAllConnections() ([]*ConnectionProcess, error)

	// Start 启动检测器
	Start() error

	// Stop 停止检测器
	Stop() error
}

// NewDetector 创建进程检测器
func NewDetector() (Detector, error) {
	return newPlatformDetector()
}

// 跨平台通用函数

// FormatAddr 格式化网络地址
func FormatAddr(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

// ParseTCPAddr 解析TCP地址字符串
func ParseTCPAddr(addrStr string) (*net.TCPAddr, error) {
	addr, err := net.ResolveTCPAddr("tcp", addrStr)
	if err != nil {
		return nil, err
	}
	return addr, nil
}

// portOf 提取 net.Addr 的端口(各平台检测器的目标查找共用)。
func portOf(a net.Addr) int {
	if a == nil {
		return -1
	}
	if t, ok := a.(*net.TCPAddr); ok {
		return t.Port
	}
	_, ps, err := net.SplitHostPort(a.String())
	if err != nil {
		return -1
	}
	p, err := strconv.Atoi(ps)
	if err != nil {
		return -1
	}
	return p
}
