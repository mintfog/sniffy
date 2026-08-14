// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/pkg/process"
)

// fakeProcessDetector 替换真实进程检测器:本平台的实现 Start/Stop 永不失败,
// 只有替身才能触发监听器里"检测器启停失败仅记日志、不影响生命周期"的分支。
type fakeProcessDetector struct {
	mu       sync.Mutex
	started  int
	stopped  int
	startErr error
	stopErr  error
}

func (d *fakeProcessDetector) GetProcessByConnection(net.Addr, net.Addr) (*process.ProcessInfo, error) {
	return nil, errors.New("unsupported in tests")
}

func (d *fakeProcessDetector) GetProcessByPID(uint32) (*process.ProcessInfo, error) {
	return nil, errors.New("unsupported in tests")
}

func (d *fakeProcessDetector) GetAllConnections() ([]*process.ConnectionProcess, error) {
	return nil, errors.New("unsupported in tests")
}

func (d *fakeProcessDetector) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.started++
	return d.startErr
}

func (d *fakeProcessDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped++
	return d.stopErr
}

func (d *fakeProcessDetector) counts() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.started, d.stopped
}

// acceptStep 描述 scriptedListener 的一次 Accept 结果;before 在结果返回前执行,
// 用于精确控制"第几次 Accept 之后 context 才被取消"。
type acceptStep struct {
	conn   net.Conn
	err    error
	before func()
}

// scriptedListener 按脚本逐次返回 Accept 结果,脚本耗尽后一直阻塞:
// 这样 accept 循环若在本该退出时继续循环,测试会以超时失败而不是悄悄通过。
type scriptedListener struct {
	mu    sync.Mutex
	steps []acceptStep
	next  int
	block chan struct{}
}

func newScriptedListener(steps ...acceptStep) *scriptedListener {
	return &scriptedListener{steps: steps, block: make(chan struct{})}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.next >= len(l.steps) {
		l.mu.Unlock()
		<-l.block
		return nil, net.ErrClosed
	}
	step := l.steps[l.next]
	l.next++
	l.mu.Unlock()

	if step.before != nil {
		step.before()
	}
	return step.conn, step.err
}

func (l *scriptedListener) Close() error   { return nil }
func (l *scriptedListener) Addr() net.Addr { return testAddr("scripted") }

func (l *scriptedListener) accepted() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.next
}

// acceptTimeoutError 模拟 SetDeadline 触发的可恢复 Accept 超时。
type acceptTimeoutError struct{}

func (acceptTimeoutError) Error() string   { return "accept deadline exceeded" }
func (acceptTimeoutError) Timeout() bool   { return true }
func (acceptTimeoutError) Temporary() bool { return true }

// runAcceptLoop 跑一轮 accept 循环并断言它会自行退出。
func runAcceptLoop(t *testing.T, tl *TCPListener, ctx context.Context) {
	t.Helper()
	done := make(chan struct{})
	tl.wg.Add(1)
	go func() {
		defer close(done)
		tl.acceptConnections(ctx)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("acceptConnections 没有退出")
	}
}

func TestTCPListenerStartStopDrivesProcessDetector(t *testing.T) {
	detector := &fakeProcessDetector{}
	tl := newTestTCPListener(defaultTestConfig())
	logger := &recordingLogger{}
	tl.SetLogger(logger)
	tl.processDetector = detector

	if err := tl.Start(); err != nil {
		t.Fatalf("Start returned %v", err)
	}
	if err := tl.Stop(); err != nil {
		t.Fatalf("Stop returned %v", err)
	}

	if started, stopped := detector.counts(); started != 1 || stopped != 1 {
		t.Fatalf("detector calls = %d start, %d stop, want 1 each", started, stopped)
	}
	for _, fragment := range []string{"Process detector started", "Process detector stopped"} {
		if !logger.contains(fragment) {
			t.Errorf("missing detector log containing %q", fragment)
		}
	}
}

func TestTCPListenerToleratesProcessDetectorFailure(t *testing.T) {
	detector := &fakeProcessDetector{
		startErr: errors.New("start boom"),
		stopErr:  errors.New("stop boom"),
	}
	tl := newTestTCPListener(defaultTestConfig())
	logger := &recordingLogger{}
	tl.SetLogger(logger)
	tl.processDetector = detector

	if err := tl.Start(); err != nil {
		t.Fatalf("检测器启动失败不应让 Start 失败,得到 %v", err)
	}
	if !tl.IsRunning() {
		t.Fatal("listener should still be running after a detector failure")
	}
	if err := tl.Stop(); err != nil {
		t.Fatalf("检测器停止失败不应让 Stop 失败,得到 %v", err)
	}
	if tl.IsRunning() {
		t.Fatal("listener should be stopped")
	}

	for _, fragment := range []string{
		"Failed to start process detector: start boom",
		"Failed to stop process detector: stop boom",
	} {
		if !logger.contains(fragment) {
			t.Errorf("missing detector failure log containing %q", fragment)
		}
	}
	if logger.contains("Process detector started") || logger.contains("Process detector stopped") {
		t.Error("失败路径不应记录成功日志")
	}
}

func TestTCPListenerStopGivesUpAfterGrace(t *testing.T) {
	tl := newTestTCPListener(defaultTestConfig())
	logger := &recordingLogger{}
	tl.SetLogger(logger)
	tl.isRunning = true

	// 模拟一个卡在其他阻塞点、不会响应连接切断的处理 goroutine:
	// Stop 对它只做有界等待,不能被拖住。
	tl.wg.Add(1)
	start := time.Now()
	err := tl.Stop()
	elapsed := time.Since(start)
	tl.wg.Done()

	if err != nil {
		t.Fatalf("Stop returned %v", err)
	}
	if elapsed < shutdownGrace {
		t.Fatalf("Stop 只等了 %v 就返回,应至少等待 %v", elapsed, shutdownGrace)
	}
	if elapsed > 4*shutdownGrace {
		t.Fatalf("Stop 等待 %v,远超 %v 的上界", elapsed, shutdownGrace)
	}
	if !logger.contains("强制退出") {
		t.Error("超时放弃等待时应记录一条错误日志")
	}
	if tl.IsRunning() {
		t.Error("listener should be marked stopped even when the wait times out")
	}
	if tl.ctx.Err() == nil {
		t.Error("Stop 应取消 context")
	}
}

func TestAcceptConnectionsRetriesOnTimeout(t *testing.T) {
	tl := newTestTCPListener(defaultTestConfig())
	handler := &stubPacketHandler{}
	tl.handler = handler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener := newScriptedListener(
		acceptStep{err: acceptTimeoutError{}},
		acceptStep{err: acceptTimeoutError{}, before: cancel},
	)
	tl.listener = listener
	runAcceptLoop(t, tl, ctx)

	if got := listener.accepted(); got != 2 {
		t.Fatalf("Accept 调用了 %d 次,want 2:超时属于可恢复错误,应继续循环", got)
	}
	if len(handler.errors) != 0 {
		t.Fatalf("超时不应上报为错误,得到 %#v", handler.errors)
	}
}

func TestAcceptConnectionsReportsPermanentError(t *testing.T) {
	tl := newTestTCPListener(defaultTestConfig())
	handler := &stubPacketHandler{}
	tl.handler = handler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener := newScriptedListener(
		acceptStep{err: errors.New("boom")},
		// 取消发生在返回之前,循环应据此退出而不是把关闭当成异常再上报一次。
		acceptStep{err: net.ErrClosed, before: cancel},
	)
	tl.listener = listener
	runAcceptLoop(t, tl, ctx)

	if got := listener.accepted(); got != 2 {
		t.Fatalf("Accept 调用了 %d 次,want 2", got)
	}
	if len(handler.errors) != 1 || !strings.Contains(handler.errors[0], "acceptConnections: accept connection failed: boom") {
		t.Fatalf("handler errors = %#v", handler.errors)
	}
}

func TestAcceptConnectionsDropsConnectionWhileClosing(t *testing.T) {
	tl := newTestTCPListener(defaultTestConfig())
	conn := newMemoryConn(nil)
	// closeAllConns 把监听器置为关闭中,此后到达的连接必须被直接切断。
	tl.closeAllConns()

	listener := newScriptedListener(acceptStep{conn: conn})
	tl.listener = listener
	// 脚本只有一步:若连接被误当作正常连接受理,循环会再取一次 Accept 并永久阻塞,
	// runAcceptLoop 随即超时报错。
	runAcceptLoop(t, tl, context.Background())

	if got := listener.accepted(); got != 1 {
		t.Fatalf("Accept 调用了 %d 次,want 1", got)
	}
	if !conn.isClosed() {
		t.Error("关闭期间收到的连接应被立即关闭")
	}
	tl.connMu.Lock()
	tracked := len(tl.conns)
	tl.connMu.Unlock()
	if tracked != 0 {
		t.Errorf("tracked connections = %d, want 0", tracked)
	}
}

var _ process.Detector = (*fakeProcessDetector)(nil)
var _ net.Listener = (*scriptedListener)(nil)
var _ net.Error = acceptTimeoutError{}
