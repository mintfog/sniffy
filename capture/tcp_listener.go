// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/plugins"
)

// TCPListener TCP监听器结构体
type TCPListener struct {
	config       Config
	listener     net.Listener
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.RWMutex
	isRunning    bool
	handler      PacketHandler
	logger       Logger
	hookExecutor *plugins.HookExecutor // 插件钩子执行器
}

// NewTCPListener 创建新的TCP监听器
func NewTCPListener(config Config) *TCPListener {
	ctx, cancel := context.WithCancel(context.Background())
	handler := NewDefaultPacketHandler(config)

	return &TCPListener{
		config:  config,
		handler: handler,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// SetLogger 设置日志器
func (tl *TCPListener) SetLogger(logger Logger) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.logger = logger
}

// SetHookExecutor 设置插件钩子执行器
func (tl *TCPListener) SetHookExecutor(hookExecutor *plugins.HookExecutor) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.hookExecutor = hookExecutor
}

// GetHandler 获取数据包处理器
func (tl *TCPListener) GetHandler() PacketHandler {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return tl.handler
}

// GetConfig 获取配置
func (tl *TCPListener) GetConfig() Config {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return tl.config
}

// Start 启动TCP监听器
func (tl *TCPListener) Start() error {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	if tl.isRunning {
		return fmt.Errorf("TCP listener is already running")
	}

	addr := fmt.Sprintf("%s:%d", tl.config.GetAddress(), tl.config.GetPort())
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start TCP listener on %s: %w", addr, err)
	}

	tl.listener = listener
	tl.isRunning = true

	tl.logInfo("TCP listener started on %s", addr)

	// 启动接受连接的goroutine
	tl.wg.Add(tl.config.GetThreads())
	for i := 0; i < tl.config.GetThreads(); i++ {
		go tl.acceptConnections()
	}

	return nil
}

// Stop 停止TCP监听器
func (tl *TCPListener) Stop() error {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	if !tl.isRunning {
		return nil
	}

	tl.logInfo("Stopping TCP listener...")

	// 取消context
	tl.cancel()

	// 关闭监听器
	if tl.listener != nil {
		tl.listener.Close()
	}

	tl.isRunning = false

	// 等待所有goroutine结束
	tl.wg.Wait()

	tl.logInfo("TCP listener stopped")
	return nil
}

// IsRunning 检查监听器是否正在运行
func (tl *TCPListener) IsRunning() bool {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return tl.isRunning
}

// GetAddress 获取监听地址
func (tl *TCPListener) GetAddress() string {
	if tl.listener != nil {
		return tl.listener.Addr().String()
	}
	return fmt.Sprintf("%s:%d", tl.config.GetAddress(), tl.config.GetPort())
}

// acceptConnections 接受连接的主循环
func (tl *TCPListener) acceptConnections() {
	defer tl.wg.Done()

	for {
		select {
		case <-tl.ctx.Done():
			return
		default:
			// 设置接受连接的超时
			if tcpListener, ok := tl.listener.(*net.TCPListener); ok {
				tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
			}

			conn, err := tl.listener.Accept()
			if err != nil {
				// 检查是否是因为超时或监听器关闭
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					continue
				}
				if tl.ctx.Err() != nil {
					return
				}

				// 处理其他错误
				tl.handleError(fmt.Errorf("accept connection failed: %w", err), "acceptConnections")
				continue
			}

			// 处理新连接
			tl.wg.Add(1)
			go tl.handleConnection(conn)
		}
	}
}

// handleConnection 处理单个连接
func (tl *TCPListener) handleConnection(conn net.Conn) {
	defer tl.wg.Done()
	defer conn.Close()

	startTime := time.Now()

	// 创建连接信息
	connInfo := &ConnectionInfo{
		LocalAddr:    conn.LocalAddr(),
		RemoteAddr:   conn.RemoteAddr(),
		StartTime:    startTime,
		BufferSize:   tl.config.GetBufferSize(),
		ReadTimeout:  tl.config.GetReadTimeout(),
		WriteTimeout: tl.config.GetWriteTimeout(),
	}

	tl.logInfo("New connection from %s", conn.RemoteAddr().String())

	// 创建连接抽象用于插件
	connection := types.NewConnection(conn, tl.handler.(*SimplePacketHandler))
	defer connection.Close()

	// 调用插件连接开始钩子
	if tl.hookExecutor != nil {
		if err := tl.hookExecutor.ExecuteConnectionStartHooks(tl.ctx, connection); err != nil {
			tl.handleError(err, "ExecuteConnectionStartHooks")
		}
	}

	// 调用处理器的连接开始回调
	if err := tl.handler.OnConnectionStart(conn); err != nil {
		tl.handleError(err, "OnConnectionStart")
		return
	}

	// 处理连接
	tl.handler.HandleConnection(conn, connInfo)

	// 调用连接结束回调
	duration := time.Since(startTime)
	tl.handler.OnConnectionEnd(conn, duration)

	// 调用插件连接结束钩子
	if tl.hookExecutor != nil {
		if err := tl.hookExecutor.ExecuteConnectionEndHooks(tl.ctx, connection, duration); err != nil {
			tl.handleError(err, "ExecuteConnectionEndHooks")
		}
	}
}

// handleError 处理错误
func (tl *TCPListener) handleError(err error, context string) {
	if tl.handler != nil {
		tl.handler.HandleError(err, context)
	} else {
		tl.logError("TCP listener error in %s: %v", context, err)
	}
}

// logInfo 记录信息日志
func (tl *TCPListener) logInfo(format string, args ...interface{}) {
	if tl.logger != nil {
		tl.logger.Info(format, args...)
	} else if tl.config.IsLoggingEnabled() {
		log.Printf(format, args...)
	}
}

// logError 记录错误日志
func (tl *TCPListener) logError(format string, args ...interface{}) {
	if tl.logger != nil {
		tl.logger.Error(format, args...)
	} else {
		log.Printf("ERROR: "+format, args...)
	}
}
