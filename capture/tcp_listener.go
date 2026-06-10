// Copyright 2025 The mintfog Authors
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
	"github.com/mintfog/sniffy/pkg/process"
	"github.com/mintfog/sniffy/plugins"
)

// shutdownGrace 是 Stop 等待连接处理 goroutine 退出的最长时间。
// 正常情况下连接被强制切断后 goroutine 会在毫秒级退出;此超时仅作兜底,
// 防止个别 goroutine 卡在插件钩子等其他阻塞点导致关闭卡死。
const shutdownGrace = 2 * time.Second

// TCPListener TCP监听器结构体
type TCPListener struct {
	config          Config
	listener        net.Listener
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	mu              sync.RWMutex
	isRunning       bool
	handler         PacketHandler
	logger          Logger
	hookExecutor    *plugins.HookExecutor // 插件钩子执行器
	processDetector process.Detector      // 进程检测器

	// 活跃连接跟踪:用于在 Stop 时强制切断所有连接,使阻塞在读写上的
	// 处理 goroutine 立即返回,避免等待连接自然结束而卡死退出。
	connMu  sync.Mutex
	conns   map[net.Conn]struct{}
	closing bool
}

// NewTCPListener 创建新的TCP监听器
func NewTCPListener(config Config) *TCPListener {
	ctx, cancel := context.WithCancel(context.Background())
	handler := NewDefaultPacketHandler(config)

	// 创建进程检测器
	detector, err := process.NewDetector()
	if err != nil {
		log.Printf("Warning: Failed to create process detector: %v", err)
		detector = nil
	}

	return &TCPListener{
		config:          config,
		handler:         handler,
		ctx:             ctx,
		cancel:          cancel,
		processDetector: detector,
		conns:           make(map[net.Conn]struct{}),
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

	// 复位连接跟踪状态。
	tl.connMu.Lock()
	tl.closing = false
	tl.conns = make(map[net.Conn]struct{})
	tl.connMu.Unlock()

	// 启动进程检测器
	if tl.processDetector != nil {
		if err := tl.processDetector.Start(); err != nil {
			tl.logError("Failed to start process detector: %v", err)
		} else {
			tl.logInfo("Process detector started")
		}
	}

	tl.logInfo("TCP listener started on %s", addr)

	// 启动接受连接的goroutine
	tl.wg.Add(tl.config.GetThreads())
	for i := 0; i < tl.config.GetThreads(); i++ {
		go tl.acceptConnections()
	}

	return nil
}

// Stop 停止TCP监听器。
//
// 关闭策略:取消 context、关闭监听 socket(阻止新连接)后,立即强制切断所有活跃连接,
// 使阻塞在读写上的处理 goroutine 立即返回;随后对 goroutine 退出做有界等待(shutdownGrace),
// 超时即直接返回。这样无论是否存在长连接(keep-alive / WebSocket / 流式响应),
// 关闭都不会卡住等待连接自然结束。
func (tl *TCPListener) Stop() error {
	tl.mu.Lock()
	if !tl.isRunning {
		tl.mu.Unlock()
		return nil
	}

	tl.logInfo("Stopping TCP listener...")

	// 取消context
	tl.cancel()

	// 关闭监听器(停止接收新连接)
	if tl.listener != nil {
		tl.listener.Close()
	}

	tl.isRunning = false

	// 停止进程检测器
	if tl.processDetector != nil {
		if err := tl.processDetector.Stop(); err != nil {
			tl.logError("Failed to stop process detector: %v", err)
		} else {
			tl.logInfo("Process detector stopped")
		}
	}
	tl.mu.Unlock()

	// 强制切断所有活跃连接:让阻塞在 Read/Write 上的处理 goroutine 立即返回。
	tl.closeAllConns()

	// 有界等待 goroutine 退出:连接已切断,正常会迅速结束;
	// 若个别 goroutine 卡在其他阻塞点,最多等待 shutdownGrace 后直接返回,绝不卡死退出。
	done := make(chan struct{})
	go func() {
		tl.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownGrace):
		tl.logError("部分连接处理未在 %s 内结束,强制退出", shutdownGrace)
	}

	tl.logInfo("TCP listener stopped")
	return nil
}

// trackConn 登记一个活跃连接;若监听器已在关闭中则返回 false(调用方应直接关闭该连接)。
func (tl *TCPListener) trackConn(conn net.Conn) bool {
	tl.connMu.Lock()
	defer tl.connMu.Unlock()
	if tl.closing {
		return false
	}
	tl.conns[conn] = struct{}{}
	return true
}

// untrackConn 注销一个连接(连接处理结束时调用)。
func (tl *TCPListener) untrackConn(conn net.Conn) {
	tl.connMu.Lock()
	delete(tl.conns, conn)
	tl.connMu.Unlock()
}

// closeAllConns 标记关闭并强制切断所有当前活跃连接。
func (tl *TCPListener) closeAllConns() {
	tl.connMu.Lock()
	tl.closing = true
	conns := tl.conns
	tl.conns = make(map[net.Conn]struct{})
	tl.connMu.Unlock()

	for c := range conns {
		c.Close()
	}
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

			// 登记连接;若已在关闭中则直接丢弃,避免泄漏未被切断的连接。
			if !tl.trackConn(conn) {
				conn.Close()
				return
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
	defer tl.untrackConn(conn)
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

	// 尝试获取进程信息
	// if tl.processDetector != nil {
	// 	if processInfo, err := tl.processDetector.GetProcessByConnection(conn.LocalAddr(), conn.RemoteAddr()); err == nil {
	// 		connInfo.ProcessName = processInfo.Name
	// 		connInfo.ProcessID = processInfo.PID
	// 		connInfo.ProcessPath = processInfo.Path
	// 		connInfo.ProcessUser = processInfo.User
	// 		tl.logInfo("Connection from %s - Process: %s (PID: %d)",
	// 			conn.RemoteAddr().String(), processInfo.Name, processInfo.PID)
	// 	} else {
	// 		tl.logInfo("Could not determine process for connection from %s: %v",
	// 			conn.RemoteAddr().String(), err)
	// 	}
	// }

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
