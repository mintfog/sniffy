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
	"github.com/mintfog/sniffy/plugins"
	"github.com/mintfog/sniffy/plugins/examples"
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

	// 初始化插件系统
	var pluginManager *plugins.PluginManager
	var hookExecutor *plugins.HookExecutor
	
	if config.Plugins.Enabled {
		pluginManager, hookExecutor = initializePluginSystem(config)
		if pluginManager != nil {
			defer func() {
				if err := pluginManager.Shutdown(); err != nil {
					log.Printf("插件系统关闭失败: %v", err)
				}
			}()
		}
	}

	// 创建TCP监听器
	listener := capture.NewTCPListener(config)
	
	// 如果插件系统启用，将钩子执行器注入到监听器
	if hookExecutor != nil {
		listener.SetHookExecutor(hookExecutor)
		
		// 同时设置到数据包处理器
		if handler := listener.GetHandler(); handler != nil {
			if simpleHandler, ok := handler.(*capture.SimplePacketHandler); ok {
				simpleHandler.SetHookExecutor(hookExecutor)
			}
		}
		
		log.Printf("插件系统已启用，钩子执行器已注入")
	}

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

// initializePluginSystem 初始化插件系统
func initializePluginSystem(config *Config) (*plugins.PluginManager, *plugins.HookExecutor) {
	log.Println("正在初始化插件系统...")

	// 创建简单的日志器
	logger := &SimpleLogger{}

	// 创建插件API
	pluginAPI := plugins.NewAPIImplementation(config, logger)

	// 创建插件管理器配置
	managerConfig := plugins.ManagerConfig{
		PluginsDir:      config.Plugins.PluginsDir,
		ConfigDir:       config.Plugins.ConfigDir,
		AutoLoad:        config.Plugins.AutoLoad,
		LoadTimeout:     time.Duration(config.Plugins.LoadTimeout) * time.Second,
		EnableHotReload: config.Plugins.EnableHotReload,
		WatchInterval:   5 * time.Second,
	}

	// 创建插件管理器
	manager := plugins.NewPluginManager(pluginAPI, logger, managerConfig)

	// 注册示例插件
	examples.RegisterExamplePlugins(manager)

	// 加载插件
	if err := manager.LoadPlugins(); err != nil {
		log.Printf("加载插件失败: %v", err)
		return nil, nil
	}

	// 启动插件
	if err := manager.StartPlugins(); err != nil {
		log.Printf("启动插件失败: %v", err)
		return nil, nil
	}

	// 创建钩子执行器
	hookExecutor := plugins.NewHookExecutor(manager, logger)

	log.Printf("插件系统初始化完成，已加载 %d 个插件", len(manager.GetPluginList()))
	
	// 打印插件信息
	for _, metadata := range manager.GetPluginList() {
		log.Printf("插件: %s v%s - %s", 
			metadata.Info.Name, 
			metadata.Info.Version, 
			metadata.Info.Description)
	}

	return manager, hookExecutor
}

// SimpleLogger 简单日志器实现
type SimpleLogger struct{}

func (sl *SimpleLogger) Info(msg string, args ...interface{}) {
	log.Printf("[INFO] "+msg, args...)
}

func (sl *SimpleLogger) Error(msg string, args ...interface{}) {
	log.Printf("[ERROR] "+msg, args...)
}

func (sl *SimpleLogger) Debug(msg string, args ...interface{}) {
	if *verbose {
		log.Printf("[DEBUG] "+msg, args...)
	}
}

func (sl *SimpleLogger) Warn(msg string, args ...interface{}) {
	log.Printf("[WARN] "+msg, args...)
}
