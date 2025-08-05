// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mintfog/sniffy/capture"
)

var (
	// 命令行参数
	listenAddr = flag.String("addr", "0.0.0.0", "TCP监听地址")
	listenPort = flag.Int("port", 8080, "TCP监听端口")
	verbose    = flag.Bool("v", false, "启用详细日志输出")
	configFile = flag.String("config", "", "配置文件路径")
)

func main() {
	flag.Parse()

	// 设置日志格式
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Println("Starting sniffy-core...")

	// 创建配置
	config := DefaultConfig()
	config.Address = *listenAddr
	config.Port = *listenPort
	config.EnableLogging = *verbose

	// 验证配置
	if err := config.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// 创建TCP监听器
	listener := capture.NewTCPListener(config)

	// 启动TCP监听器
	if err := listener.Start(); err != nil {
		log.Fatalf("Failed to start TCP listener: %v", err)
	}

	// 监听系统信号
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("sniffy-core is running on %s", config.GetListenAddress())
	log.Println("Press Ctrl+C to stop...")

	// 等待关闭信号
	<-signalChan

	log.Println("Received shutdown signal, gracefully shutting down...")

	// 创建关闭超时context
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// 在goroutine中执行关闭操作
	shutdownComplete := make(chan struct{})
	go func() {
		defer close(shutdownComplete)

		// 停止TCP监听器
		if err := listener.Stop(); err != nil {
			log.Printf("Error stopping TCP listener: %v", err)
		}

		log.Println("All services stopped successfully")
	}()

	// 等待关闭完成或超时
	select {
	case <-shutdownComplete:
		log.Println("Shutdown completed")
	case <-shutdownCtx.Done():
		log.Println("Shutdown timeout exceeded, force exiting")
	}

	os.Exit(0)
}
