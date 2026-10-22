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
	"net/netip"
	"sync"
	"time"

	"github.com/mintfog/sniffy/pkg/process"
)

// shutdownGrace 是 Stop 等待连接处理 goroutine 退出的最长时间。
// 正常情况下连接被强制切断后 goroutine 会在毫秒级退出;此超时仅作兜底,
// 防止个别 goroutine 卡在断点等待等其他阻塞点导致关闭卡死。
const shutdownGrace = 2 * time.Second

// ErrProxyLoop 标识实际拨号结果指向当前代理监听器。
var ErrProxyLoop = errors.New("拒绝连接代理自身的监听端口")

var outboundDialer = net.Dialer{Timeout: 30 * time.Second}

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
	processDetector process.Detector // 进程检测器

	// 活跃连接跟踪:用于在 Stop 时强制切断所有连接,使阻塞在读写上的
	// 处理 goroutine 立即返回,避免等待连接自然结束而卡死退出。
	connMu  sync.Mutex
	conns   map[net.Conn]struct{}
	closing bool

	// 通配监听可通过多个本机 IP 接入；按入站端点计数，供出站连接识别自身。
	localEndpoints map[netip.AddrPort]int
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

// SetLogger 设置日志器,并同步注入内部数据包处理器。
// 处理器拿不到 logger 时会退回无等级过滤的 log.Output 兜底,
// 导致 Debug 日志在任何模式下都全量输出。
func (tl *TCPListener) SetLogger(logger Logger) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.logger = logger
	if h, ok := tl.handler.(*SimplePacketHandler); ok {
		h.SetLogger(logger)
	}
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

	// Stop 取消了上一轮的 ctx,重启必须换一个新的:否则 accept 循环一进入就从
	// ctx.Done() 返回,端口绑上了、IsRunning 也为真,却没有任何 goroutine 在服务。
	ctx, cancel := context.WithCancel(context.Background())
	tl.ctx, tl.cancel = ctx, cancel

	// 复位连接跟踪状态。
	tl.connMu.Lock()
	tl.closing = false
	tl.conns = make(map[net.Conn]struct{})
	tl.localEndpoints = make(map[netip.AddrPort]int)
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
		// 传入本轮的 ctx 而非让 goroutine 读 tl.ctx:处理 goroutine 不再触碰该字段,
		// 与下一轮 Start 的写入天然无竞态(Stop 的等待是有界的,可能有残留 goroutine)。
		go tl.acceptConnections(ctx)
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
	if _, tracked := tl.conns[conn]; tracked {
		return true
	}
	tl.conns[conn] = struct{}{}
	if local, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		if tl.localEndpoints == nil {
			tl.localEndpoints = make(map[netip.AddrPort]int)
		}
		tl.localEndpoints[local.AddrPort()]++
	}
	return true
}

// untrackConn 注销一个连接(连接处理结束时调用)。
func (tl *TCPListener) untrackConn(conn net.Conn) {
	tl.connMu.Lock()
	defer tl.connMu.Unlock()
	if _, tracked := tl.conns[conn]; !tracked {
		return
	}
	delete(tl.conns, conn)
	if local, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		endpoint := local.AddrPort()
		tl.localEndpoints[endpoint]--
		if tl.localEndpoints[endpoint] == 0 {
			delete(tl.localEndpoints, endpoint)
		}
	}
}

// closeAllConns 标记关闭并强制切断所有当前活跃连接。
func (tl *TCPListener) closeAllConns() {
	tl.connMu.Lock()
	tl.closing = true
	conns := tl.conns
	tl.conns = make(map[net.Conn]struct{})
	tl.localEndpoints = nil
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

// DialContext 在发送任何协议字节前校验实际拨号结果，覆盖域名解析与上游代理首跳。
func (tl *TCPListener) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := outboundDialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if tl.isProxyLoop(conn) {
		_ = conn.Close()
		return nil, ErrProxyLoop
	}
	return conn, nil
}

func (tl *TCPListener) isProxyLoop(conn net.Conn) bool {
	remote, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return false
	}
	tl.mu.RLock()
	var bound *net.TCPAddr
	if tl.isRunning && tl.listener != nil {
		bound, _ = tl.listener.Addr().(*net.TCPAddr)
	}
	tl.mu.RUnlock()
	if bound == nil || bound.Port != remote.Port {
		return false
	}
	if bound.IP.Equal(remote.IP) {
		return true
	}
	if !bound.IP.IsUnspecified() {
		return false
	}
	local, _ := conn.LocalAddr().(*net.TCPAddr)
	if remote.IP.IsLoopback() || (local != nil && local.IP.Equal(remote.IP)) {
		return true
	}
	tl.connMu.Lock()
	owned := tl.localEndpoints[remote.AddrPort()] > 0
	tl.connMu.Unlock()
	return owned
}

// acceptConnections 接受连接的主循环
func (tl *TCPListener) acceptConnections(ctx context.Context) {
	defer tl.wg.Done()

	tl.mu.RLock()
	listener := tl.listener
	tl.mu.RUnlock()
	var retryDelay time.Duration
	for {
		if ctx.Err() != nil {
			return
		}
		if tcpListener, ok := listener.(*net.TCPListener); ok {
			tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}

			tl.handleError(fmt.Errorf("accept connection failed: %w", err), "acceptConnections")
			if netErr == nil || !netErr.Temporary() {
				return
			}
			// 描述符耗尽等临时错误会立即返回；指数退避避免错误风暴占满 CPU。
			retryDelay = min(max(5*time.Millisecond, retryDelay*2), time.Second)
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		retryDelay = 0

		// 登记连接;若已在关闭中则直接丢弃,避免泄漏未被切断的连接。
		if !tl.trackConn(conn) {
			conn.Close()
			return
		}

		tl.wg.Add(1)
		go tl.handleConnection(conn)
	}
}

// handleConnection 处理单个连接
func (tl *TCPListener) handleConnection(conn net.Conn) {
	defer tl.wg.Done()
	defer tl.untrackConn(conn)
	defer conn.Close()
	conn = wrapThrottleConn(conn)

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

	// 每连接日志属于热路径,降为 Debug,避免高并发时日志写入拖慢连接处理。
	tl.logDebug("New connection from %s", conn.RemoteAddr().String())

	// 调用处理器的连接开始回调
	if err := tl.handler.OnConnectionStart(conn); err != nil {
		tl.handleError(err, "OnConnectionStart")
		return
	}

	// 处理连接
	tl.handler.HandleConnection(conn, connInfo)

	// 调用连接结束回调
	tl.handler.OnConnectionEnd(conn, time.Since(startTime))
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

// logDebug 记录调试日志(无 logger 时不输出)
func (tl *TCPListener) logDebug(format string, args ...interface{}) {
	if tl.logger != nil {
		tl.logger.Debug(format, args...)
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
