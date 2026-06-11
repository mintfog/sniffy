// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mintfog/sniffy/internal/api"
	"github.com/mintfog/sniffy/internal/app"
)

var (
	listenAddr = flag.String("addr", "0.0.0.0", "代理监听地址")
	listenPort = flag.Int("port", 8080, "代理监听端口")
	apiPort    = flag.Int("api-port", 8888, "管理 API(HTTP+WebSocket)端口")
	verbose    = flag.Bool("v", false, "启用详细日志输出")
	_          = flag.String("config", "", "配置文件路径(预留)")
)

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("启动 sniffy(headless 服务器模式)...")

	// 创建并校验配置:默认值 < 持久化配置(config.json) < 命令行显式参数。
	config := DefaultConfig()
	config.Address, config.Port = app.ResolveListen(config.Address, config.Port)
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr":
			config.Address = *listenAddr
		case "port":
			config.Port = *listenPort
		}
	})
	config.EnableLogging = *verbose
	if err := config.Validate(); err != nil {
		app.Fatalf("配置无效: %v", err)
	}

	// 装配核心组件(引擎 + 服务 + 管道 + 插件)。
	application, err := app.Build(config, *verbose)
	if err != nil {
		app.Fatalf("初始化失败: %v", err)
	}

	// 启动抓包引擎。
	if err := application.Start(); err != nil {
		app.Fatalf("启动引擎失败: %v", err)
	}

	// 启动管理 API(HTTP + WebSocket)。
	apiAddr := fmt.Sprintf("%s:%d", "0.0.0.0", *apiPort)
	apiServer := api.New(application.Service, application.Pipeline, application.Plugins, apiAddr)
	go func() {
		log.Printf("管理 API 监听于 http://%s", apiAddr)
		if err := apiServer.Start(); err != nil && err.Error() != "http: Server closed" {
			log.Printf("管理 API 退出: %v", err)
		}
	}()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	log.Printf("sniffy 代理运行于 %s", config.GetListenAddress())
	log.Println("按 Ctrl+C 停止...")
	<-signalChan

	log.Println("收到关闭信号,正在优雅关闭...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = apiServer.Stop(shutdownCtx)
		if err := application.Stop(); err != nil {
			log.Printf("停止引擎出错: %v", err)
		}
		log.Println("所有服务已停止")
	}()

	select {
	case <-done:
		log.Println("关闭完成")
	case <-shutdownCtx.Done():
		log.Println("关闭超时,强制退出")
	}
	app.FlushLogs() // os.Exit 不走 defer,显式把缓冲日志落盘
	os.Exit(0)
}
