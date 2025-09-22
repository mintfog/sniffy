// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mintfog/sniffy/pkg/process"
)

func main() {
	fmt.Println("=== Sniffy 进程检测示例 ===")

	// 创建进程检测器
	detector, err := process.NewDetector()
	if err != nil {
		log.Fatalf("创建进程检测器失败: %v", err)
	}

	// 启动检测器
	if err := detector.Start(); err != nil {
		log.Fatalf("启动进程检测器失败: %v", err)
	}
	defer detector.Stop()

	fmt.Println("进程检测器已启动")

	// 获取所有网络连接
	fmt.Println("\n=== 获取所有网络连接 ===")
	connections, err := detector.GetAllConnections()
	if err != nil {
		log.Printf("获取网络连接失败: %v", err)
	} else {
		fmt.Printf("发现 %d 个活跃连接:\n", len(connections))

		for i, conn := range connections {
			if i >= 10 { // 只显示前10个连接
				fmt.Printf("... 还有 %d 个连接\n", len(connections)-10)
				break
			}

			fmt.Printf("\n连接 %d:\n", i+1)
			fmt.Printf("  协议: %s\n", conn.Protocol)
			fmt.Printf("  本地地址: %s\n", conn.LocalAddr.String())
			fmt.Printf("  远程地址: %s\n", conn.RemoteAddr.String())

			if conn.ProcessInfo != nil {
				fmt.Printf("  进程信息:\n")
				fmt.Printf("    PID: %d\n", conn.ProcessInfo.PID)
				fmt.Printf("    名称: %s\n", conn.ProcessInfo.Name)
				fmt.Printf("    路径: %s\n", conn.ProcessInfo.Path)
				fmt.Printf("    用户: %s\n", conn.ProcessInfo.User)
				if conn.ProcessInfo.CommandLine != "" {
					fmt.Printf("    命令行: %s\n", conn.ProcessInfo.CommandLine)
				}
			}
		}
	}

	// 测试通过PID获取进程信息
	fmt.Println("\n=== 测试PID查询 ===")
	testPIDs := []uint32{0, 4} // 0是Idle进程，4通常是System进程

	for _, pid := range testPIDs {
		fmt.Printf("\n查询PID %d:\n", pid)
		processInfo, err := detector.GetProcessByPID(pid)
		if err != nil {
			fmt.Printf("  错误: %v\n", err)
		} else {
			fmt.Printf("  名称: %s\n", processInfo.Name)
			fmt.Printf("  路径: %s\n", processInfo.Path)
			fmt.Printf("  用户: %s\n", processInfo.User)
			if processInfo.CommandLine != "" {
				fmt.Printf("  命令行: %s\n", processInfo.CommandLine)
			}
		}
	}

	// 尝试找到当前Go进程
	fmt.Println("\n=== 查找当前Go进程 ===")
	currentPid := uint32(os.Getpid())
	fmt.Printf("当前进程PID: %d\n", currentPid)
	if processInfo, err := detector.GetProcessByPID(currentPid); err == nil {
		fmt.Printf("  名称: %s\n", processInfo.Name)
		fmt.Printf("  路径: %s\n", processInfo.Path)
		fmt.Printf("  用户: %s\n", processInfo.User)
	} else {
		fmt.Printf("  获取当前进程信息失败: %v\n", err)
	}

	// 演示监控模式
	fmt.Println("\n=== 监控模式演示（5秒） ===")
	fmt.Println("正在监控网络连接变化...")

	startTime := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			connections, err := detector.GetAllConnections()
			if err != nil {
				fmt.Printf("获取连接失败: %v\n", err)
			} else {
				fmt.Printf("[%s] 当前活跃连接数: %d\n",
					time.Now().Format("15:04:05"), len(connections))
			}

			if time.Since(startTime) > 5*time.Second {
				fmt.Println("监控结束")
				return
			}
		}
	}
}
