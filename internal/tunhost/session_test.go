// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
)

// fakeHost 代替虚拟网卡：fail 模拟网卡自己失效，Close 记下被关闭。
type fakeHost struct {
	done     chan struct{}
	closed   chan struct{}
	once     sync.Once
	doneOnce sync.Once
}

func newFakeHost() *fakeHost {
	return &fakeHost{done: make(chan struct{}), closed: make(chan struct{})}
}

func (h *fakeHost) Name() string          { return "fake0" }
func (h *fakeHost) Done() <-chan struct{} { return h.done }
func (h *fakeHost) fail()                 { h.doneOnce.Do(func() { close(h.done) }) }
func (h *fakeHost) Close() error {
	h.once.Do(func() { close(h.closed) })
	h.fail()
	return nil
}

// helperRun 是一次 helper 运行：handler 是它开启网卡时拿到的连接回调，result 收到 serveHelper
// 的返回值。
type helperRun struct {
	host    *fakeHost
	handler chan func(net.Conn)
	// opts 是 helper 开启网卡时收到的参数。
	opts   chan Options
	result chan error
}

// launchFake 返回一个 launch：在本进程的 goroutine 里运行 helper，网卡换成 fakeHost。
func launchFake(startErr error) (*helperRun, Launch) {
	run := &helperRun{host: newFakeHost(), handler: make(chan func(net.Conn), 1), opts: make(chan Options, 1), result: make(chan error, 1)}
	start := func(_ netbind.Egress, opts Options, h func(net.Conn)) (Host, error) {
		run.opts <- opts
		if startErr != nil {
			return nil, startErr
		}
		run.handler <- h
		return run.host, nil
	}
	return run, func(cmd []string, _ func(error)) error {
		if len(cmd) != 6 || cmd[1] != Command || cmd[2] != "--endpoint" || cmd[4] != "--pid" || cmd[5] != strconv.Itoa(os.Getpid()) {
			return errors.New("命令行不对: " + strings.Join(cmd, " "))
		}
		go func() { run.result <- serveHelper(context.Background(), cmd[3], os.Getpid(), start) }()
		return nil
	}
}

func testEgress(t *testing.T) netbind.Egress {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil || len(ifaces) == 0 {
		t.Skip("没有可用网卡", err)
	}
	return netbind.Egress{IPv4: &ifaces[0]}
}

func waitResult(t *testing.T, ch <-chan error, d time.Duration) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(d):
		t.Fatal("helper 没有在时限内结束")
		return nil
	}
}

// tcpPair 返回一对相连的 TCP 连接，模拟虚拟网卡交来的应用连接：server 端的 RemoteAddr 是
// 应用的地址，LocalAddr 是它要连的目标。
func tcpPair(t *testing.T) (client, server *net.TCPConn) {
	t.Helper()
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan *net.TCPConn, 1)
	go func() {
		c, _ := ln.AcceptTCP()
		accepted <- c
	}()
	client, err = net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	server = <-accepted
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

// TestHelperReceivesOptions：主进程给的是否拦截 QUIC 经 start 消息原样传到 helper 开启网卡处。
func TestHelperReceivesOptions(t *testing.T) {
	t.Parallel()
	for _, want := range []Options{{BlockQUIC: true}, {}} {
		run, launch := launchFake(nil)
		s, err := startHelper(context.Background(), testEgress(t), want, func(c net.Conn) { c.Close() }, launch, absent)
		if err != nil {
			t.Fatal(err)
		}
		if got := <-run.opts; got != want {
			t.Errorf("helper 收到 %+v，应为 %+v", got, want)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHelperHandoff(t *testing.T) {
	t.Parallel()
	run, launch := launchFake(nil)
	got := make(chan net.Conn, 1)
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { got <- c }, launch, absent)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Name() != "fake0" {
		t.Errorf("网卡名 %q，应为 helper 报告的 fake0", s.Name())
	}

	app, tunSide := tcpPair(t)
	go (<-run.handler)(tunSide)
	var conn net.Conn
	select {
	case conn = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("主进程没有收到交接连接")
	}
	defer conn.Close()
	if conn.RemoteAddr().String() != tunSide.RemoteAddr().String() || conn.LocalAddr().String() != tunSide.LocalAddr().String() {
		t.Errorf("交接连接地址 %s → %s，应为 %s → %s",
			conn.RemoteAddr(), conn.LocalAddr(), tunSide.RemoteAddr(), tunSide.LocalAddr())
	}
	if _, ok := conn.RemoteAddr().(*net.TCPAddr); !ok {
		t.Errorf("RemoteAddr 是 %T，进程查询与连接跟踪要求 *net.TCPAddr", conn.RemoteAddr())
	}

	// 应用发完请求即关闭写端；主进程隔一会儿才应答（如停在断点上），应答后再关闭写端。
	_, _ = app.Write([]byte("request"))
	_ = app.CloseWrite()
	req, err := io.ReadAll(conn)
	if err != nil || string(req) != "request" {
		t.Fatalf("主进程读到 %q（%v），应为 request 后 EOF", req, err)
	}
	time.Sleep(200 * time.Millisecond)
	_, _ = conn.Write([]byte("response"))
	_ = conn.(interface{ CloseWrite() error }).CloseWrite()
	_ = app.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := io.ReadAll(app)
	if err != nil || string(resp) != "response" {
		t.Fatalf("应用读到 %q（%v），应为 response 后 EOF", resp, err)
	}
}

func TestHelperCloseRemovesDeviceFirst(t *testing.T) {
	t.Parallel()
	run, launch := launchFake(nil)
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launch, absent)
	if err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-run.host.closed:
	default:
		t.Error("Close 返回时 helper 还没有拆掉网卡")
	}
	if err := waitResult(t, run.result, 5*time.Second); err != nil {
		t.Errorf("helper 返回 %v，应正常退出", err)
	}
	if s.Err() != nil {
		t.Errorf("经 Close 停止时 Err 为 %v，应为 nil", s.Err())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("通道目录 %s 没有清理: %v", dir, err)
	}
}

func TestHelperDeviceFailure(t *testing.T) {
	t.Parallel()
	run, launch := launchFake(nil)
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launch, absent)
	if err != nil {
		t.Fatal(err)
	}
	run.host.fail()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("网卡失效后主进程没有察觉")
	}
	if s.Err() == nil || !strings.Contains(s.Err().Error(), "虚拟网卡已失效") {
		t.Errorf("Err 为 %v，应带 helper 报告的原因", s.Err())
	}
	if err := waitResult(t, run.result, 5*time.Second); err == nil {
		t.Error("网卡失效时 helper 应以错误退出")
	}
}

func TestHelperStartFailure(t *testing.T) {
	t.Parallel()
	_, launch := launchFake(errors.New("找不到 wintun.dll"))
	_, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launch, absent)
	if err == nil || !strings.Contains(err.Error(), "找不到 wintun.dll") {
		t.Errorf("得到 %v，应带 helper 报告的原因", err)
	}
}

// helper 报告开启失败后随即退出，launch 的退出通知（Windows 上只有退出码）可能先于主进程读到报告。
func TestStartFailureReasonSurvivesExitNotice(t *testing.T) {
	t.Parallel()
	for range 50 {
		_, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(cmd []string, exited func(error)) error {
			go func() {
				_ = serveHelper(context.Background(), cmd[3], os.Getpid(), func(netbind.Egress, Options, func(net.Conn)) (Host, error) {
					return nil, errors.New("找不到 wintun.dll")
				})
				exited(errors.New("helper 以退出码 1 结束"))
			}()
			return nil
		}, absent)
		if err == nil || !strings.Contains(err.Error(), "找不到 wintun.dll") {
			t.Fatalf("开启返回 %v，应带 helper 报告的原因", err)
		}
	}
}

// TestStartHelperAlreadyCanceled：开始之前就已放弃的，不运行 helper，即不弹提权确认、不打印命令。
func TestStartHelperAlreadyCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err := startHelper(ctx, testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func([]string, func(error)) error {
		t.Error("已放弃的开启仍运行了 helper")
		return nil
	}, absent)
	if s != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("得到 %v, %v，应为已取消", s, err)
	}
}

// TestStartAlreadyCanceled：已经放弃的开启什么都不做，既不在本进程里开启网卡，也不运行 helper。
func TestStartAlreadyCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	host, err := Start(ctx, testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func([]string, func(error)) error {
		t.Error("已放弃的开启仍运行了 helper")
		return nil
	})
	if host != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("得到 %v, %v，应为已取消", host, err)
	}
}

// TestStartWithoutPrivilegeUsesHelper：本进程没有权限时改经 helper 开启；helper 运行不起来时返回的
// Host 是 nil，而不是装着 nil 指针的接口。
func TestStartWithoutPrivilegeUsesHelper(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("有权限时会真的开启网卡")
	}
	launched := false
	host, err := Start(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func([]string, func(error)) error {
		launched = true
		return errors.New("用户取消了提权")
	})
	if !launched {
		t.Skipf("本进程有权限开启虚拟网卡（%v）", err)
	}
	if host != nil || err == nil || !strings.Contains(err.Error(), "用户取消了提权") {
		t.Errorf("得到 %v, %v，应为 nil 与运行 helper 失败的原因", host, err)
	}
}

func TestStartHelperCanceled(t *testing.T) {
	t.Parallel()
	var dir string
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := startHelper(ctx, testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(cmd []string, _ func(error)) error {
		dir = filepath.Dir(cmd[3])
		return nil
	}, absent)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("得到 %v，应为等待超时", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("通道目录 %s 没有清理: %v", dir, err)
	}
}

// fakeMain 模拟主进程一侧：收下控制连接、下发 start、读到 started 后返回控制连接。
func fakeMain(t *testing.T, egress netbind.Egress) (endpoint string, accept func() *control) {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "ch"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), func() *control {
		conn, err := ln.AcceptUnix()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		if _, err := io.ReadFull(conn, make([]byte, preambleLen)); err != nil {
			t.Fatal(err)
		}
		ctl := &control{conn: conn}
		if err := ctl.send(message{Type: msgStart, Egress4: egress.IPv4.Index}); err != nil {
			t.Fatal(err)
		}
		if m, err := ctl.recv(5 * time.Second); err != nil || m.Type != msgStarted {
			t.Fatalf("收到 %+v（%v），应为 started", m, err)
		}
		return ctl
	}
}

func TestHelperExitsWhenMainHangs(t *testing.T) {
	t.Parallel()
	endpoint, accept := fakeMain(t, testEgress(t))
	host := newFakeHost()
	result := make(chan error, 1)
	go func() {
		result <- serveHelper(context.Background(), endpoint, os.Getpid(), func(netbind.Egress, Options, func(net.Conn)) (Host, error) { return host, nil })
	}()
	accept() // 此后不再发 ping，如同主进程被挂起
	began := time.Now()
	err := waitResult(t, result, 3*heartbeatTimeout)
	if err == nil || !strings.Contains(err.Error(), "失联") {
		t.Errorf("helper 返回 %v，应因失联退出", err)
	}
	if d := time.Since(began); d < heartbeatTimeout-time.Second {
		t.Errorf("%v 就判定失联，应等满 %v", d, heartbeatTimeout)
	}
	select {
	case <-host.closed:
	default:
		t.Error("helper 退出前没有拆掉网卡")
	}
}

func TestHelperStopsOnSignal(t *testing.T) {
	t.Parallel()
	endpoint, accept := fakeMain(t, testEgress(t))
	host := newFakeHost()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- serveHelper(ctx, endpoint, os.Getpid(), func(netbind.Egress, Options, func(net.Conn)) (Host, error) { return host, nil })
	}()
	ctl := accept()
	cancel()
	if err := waitResult(t, result, 5*time.Second); err != nil {
		t.Errorf("helper 返回 %v，收到退出信号应正常退出", err)
	}
	select {
	case <-host.closed:
	default:
		t.Error("helper 退出前没有拆掉网卡")
	}
	if m, err := ctl.recv(time.Second); err != nil || m.Type != msgStopped {
		t.Errorf("主进程收到 %+v（%v），应为 stopped", m, err)
	}
}

func TestSessionTreatsHelperStopAsNormal(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	host := newFakeHost()
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(cmd []string, _ func(error)) error {
		go serveHelper(ctx, cmd[3], os.Getpid(), func(netbind.Egress, Options, func(net.Conn)) (Host, error) { return host, nil })
		return nil
	}, absent)
	if err != nil {
		t.Fatal(err)
	}
	cancel() // 如同 helper 收到退出信号
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("helper 停止后会话没有结束")
	}
	if s.Err() != nil {
		t.Errorf("会话 Err 为 %v，helper 正常停止应为 nil", s.Err())
	}
}

func TestStartHelperCanceledWhileStarting(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	host := newFakeHost()
	var installed atomic.Bool
	// 网卡在开启完成时出现、拆掉时消失。主进程若在开启期间就放弃，此时还查不到网卡，会在 helper
	// 装上路由之前返回。
	present := func() bool {
		select {
		case <-host.closed:
			return false
		default:
			return installed.Load()
		}
	}
	result := make(chan error, 1)
	s, err := startHelper(ctx, testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(cmd []string, _ func(error)) error {
		go func() {
			result <- serveHelper(context.Background(), cmd[3], os.Getpid(), func(netbind.Egress, Options, func(net.Conn)) (Host, error) {
				cancel() // 如同网卡开启期间用户按了 Ctrl+C
				time.Sleep(300 * time.Millisecond)
				installed.Store(true)
				return host, nil
			})
		}()
		return nil
	}, present)
	if s != nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "等待 helper 开启网卡") {
		t.Fatalf("得到会话 %v、错误 %v，应因 ctx 结束放弃开启", s, err)
	}
	select {
	case <-host.closed:
	default:
		t.Error("helper 拆掉网卡之前主进程就返回了")
	}
	if err := waitResult(t, result, 5*time.Second); err != nil {
		t.Errorf("helper 返回 %v，主进程放弃开启时应正常收尾", err)
	}
}

func TestChannelPathAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 的临时目录取自 GetTempPath")
	}
	// 通道地址转成绝对路径后受 Unix socket 的路径长度限制（macOS 104 字节），t.TempDir 往往已经
	// 太长，工作目录另建在 /tmp 下。
	wd, err := os.MkdirTemp("/tmp", "sniffy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wd) })
	t.Chdir(wd)
	t.Setenv("TMPDIR", ".")
	stop := errors.New("检查完毕")
	_, err = startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(cmd []string, _ func(error)) error {
		if !filepath.IsAbs(cmd[3]) {
			t.Errorf("通道地址 %q 是相对路径，helper 的工作目录不同时连不上", cmd[3])
		}
		return stop
	}, absent)
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
}

// launchHung 返回一个 launch：helper 完成握手后就不再收发任何消息，如同被挂起。
func launchHung(t *testing.T) Launch {
	return func(cmd []string, _ func(error)) error {
		go func() {
			raw, err := net.Dial("unix", cmd[3])
			if err != nil {
				return
			}
			t.Cleanup(func() { raw.Close() })
			_, _ = raw.Write(preamble(kindControl))
			ctl := &control{conn: raw.(*net.UnixConn)}
			if _, err := ctl.recv(time.Second); err == nil {
				_ = ctl.send(message{Type: msgStarted, Name: "hung0"})
			}
		}()
		return nil
	}
}

// absent 表示虚拟网卡不在，用于不关心网卡清理的用例。
func absent() bool { return false }

// heldNIC 模拟 helper 仍持有的网卡：release 之前一直在。
func heldNIC() (present func() bool, release func()) {
	gone := make(chan struct{})
	return func() bool {
			select {
			case <-gone:
				return false
			default:
				return true
			}
		}, func() {
			close(gone)
		}
}

func TestSessionKeepsRunningWhileHungHelperHoldsNIC(t *testing.T) {
	t.Parallel()
	present, release := heldNIC()
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launchHung(t), present)
	if err != nil {
		t.Fatal(err)
	}
	// 心跳超时之后网卡仍在，Done 不能关闭：调用方会据此撤销出站绑定。
	select {
	case <-s.Done():
		t.Fatal("helper 失去响应、网卡仍在时 Done 已关闭")
	case <-time.After(heartbeatTimeout + 2*time.Second):
	}
	release()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("网卡消失后 Done 没有关闭")
	}
	if s.Err() == nil || !strings.Contains(s.Err().Error(), "失去响应") {
		t.Errorf("Err 为 %v，应为失去响应", s.Err())
	}
}

func TestSessionCloseTimesOutWhileNICRemains(t *testing.T) {
	t.Parallel()
	present, release := heldNIC()
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launchHung(t), present)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	err = s.Close()
	if err == nil || !strings.Contains(err.Error(), "hung0") {
		t.Fatalf("Close 返回 %v，网卡仍在时应报错", err)
	}
	if d := time.Since(began); d < stopTimeout-time.Second {
		t.Errorf("Close 在 %v 后就返回，应等满 %v", d, stopTimeout)
	}
	select {
	case <-s.Done():
		t.Fatal("网卡仍在时 Done 已关闭")
	default:
	}
	release()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("网卡消失后 Done 没有关闭")
	}
}

func TestSessionRejectsStrayConnections(t *testing.T) {
	t.Parallel()
	run, launch := launchFake(nil)
	got := make(chan net.Conn, 1)
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { got <- c }, launch, absent)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 交接阶段收到非法的头也只是关闭这条连接。
	bad, err := net.Dial("unix", s.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = bad.Write(make([]byte, handoffLen))
	_ = bad.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := bad.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Errorf("非法交接头的连接读到 %d 字节（%v），应被关闭", n, err)
	}
	bad.Close()

	_, tunSide := tcpPair(t)
	go (<-run.handler)(tunSide)
	select {
	case c := <-got:
		c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("之后的正常交接没有到达")
	}
	select {
	case <-s.Done():
		t.Fatalf("会话意外结束: %v", s.Err())
	default:
	}
}

func TestPipeIdleAfterMainCloses(t *testing.T) {
	t.Parallel()
	app, tunSide := tcpPair(t)
	main, helperSide := unixPair(t)
	done := make(chan struct{})
	go func() {
		pipe(tunSide, helperSide, 200*time.Millisecond)
		close(done)
	}()
	// 主进程整个关闭交接连接，应用留着连接不发数据：一个空闲周期后断开。
	main.Close()
	_ = app.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(app); err != nil {
		t.Fatalf("应用应读到 EOF: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("交接连接已关闭、应用空闲时中继没有结束")
	}
}

func TestPipeKeepsAppHalfCloseOpen(t *testing.T) {
	t.Parallel()
	app, tunSide := tcpPair(t)
	main, helperSide := unixPair(t)
	go pipe(tunSide, helperSide, 100*time.Millisecond)
	// 应用关了写端，主进程过了好几个空闲周期才应答：连接要一直保留。
	_ = app.CloseWrite()
	if b, err := io.ReadAll(main); err != nil || len(b) != 0 {
		t.Fatalf("主进程读到 %q（%v），应直接 EOF", b, err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := main.Write([]byte("late")); err != nil {
		t.Fatal(err)
	}
	_ = main.CloseWrite()
	_ = app.SetReadDeadline(time.Now().Add(5 * time.Second))
	if b, err := io.ReadAll(app); err != nil || string(b) != "late" {
		t.Errorf("应用读到 %q（%v），应为 late", b, err)
	}
}

func TestTunPresent(t *testing.T) {
	t.Parallel()
	ipnet := func(s string) net.Addr {
		_, n, _ := net.ParseCIDR(s)
		n.IP = net.ParseIP(s[:strings.IndexByte(s, '/')])
		return n
	}
	for _, tc := range []struct {
		name  string
		addrs []net.Addr
		err   error
		want  bool
	}{
		{"枚举失败时无从确认，按仍在", nil, errors.New("netlink 暂时失败"), true},
		{"带虚拟网卡的 IPv4 地址", []net.Addr{ipnet("10.0.0.5/24"), ipnet("172.29.113.1/30")}, nil, true},
		{"只剩虚拟网卡的 IPv6 地址", []net.Addr{ipnet("fd73:6e69:6666::1/126")}, nil, true},
		{"IPv4 映射形式", []net.Addr{&net.IPNet{IP: net.ParseIP("172.29.113.1"), Mask: net.CIDRMask(30, 32)}}, nil, true},
		{"同网段的其它地址不算", []net.Addr{ipnet("172.29.113.2/30")}, nil, false},
		{"没有虚拟网卡的地址", []net.Addr{ipnet("127.0.0.1/8"), ipnet("::1/128")}, nil, false},
	} {
		got := tunPresentIn(func() ([]net.Addr, error) { return tc.addrs, tc.err })
		if got != tc.want {
			t.Errorf("%s: 得到 %v，应为 %v", tc.name, got, tc.want)
		}
	}
}

// launchBadReply 返回一个 launch：helper 收到 start 后回一条意外的消息，模拟装好网卡后才出错。
func launchBadReply(t *testing.T) Launch {
	return func(cmd []string, _ func(error)) error {
		go func() {
			raw, err := net.Dial("unix", cmd[3])
			if err != nil {
				return
			}
			t.Cleanup(func() { raw.Close() })
			_, _ = raw.Write(preamble(kindControl))
			ctl := &control{conn: raw.(*net.UnixConn)}
			if _, err := ctl.recv(time.Second); err == nil {
				_ = ctl.send(message{Type: "bogus"})
			}
		}()
		return nil
	}
}

func TestStartFailureWaitsForNIC(t *testing.T) {
	t.Parallel()
	present, release := heldNIC()
	began := time.Now()
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launchBadReply(t), present)
	if err == nil || s == nil {
		t.Fatalf("得到会话 %v、错误 %v；网卡仍在时应连同会话一起报错", s, err)
	}
	if d := time.Since(began); d < stopTimeout-time.Second {
		t.Errorf("%v 就放弃等待，应等满 %v", d, stopTimeout)
	}
	select {
	case <-s.Done():
		t.Fatal("网卡仍在时 Done 已关闭")
	default:
	}
	release()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("网卡消失后 Done 没有关闭")
	}
	if s.Err() != nil {
		t.Errorf("开启失败的会话 Err 为 %v，失败原因已由 StartHelper 返回", s.Err())
	}
}

func TestStartFailureAfterNICGone(t *testing.T) {
	t.Parallel()
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launchBadReply(t), absent)
	if err == nil || s != nil {
		t.Errorf("得到会话 %v、错误 %v；确认网卡已消失时只应返回错误", s, err)
	}
}

// helper 开网卡途中挂起、主进程等不及关闭通道退出后，helper 恢复运行时要自己拆掉网卡：主进程
// 已经不在，没有别人能清理路由。
func TestHelperTearsDownWhenMainLeftDuringStart(t *testing.T) {
	t.Parallel()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "ch"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	host := newFakeHost()
	resume := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- serveHelper(context.Background(), ln.Addr().String(), os.Getpid(), func(netbind.Egress, Options, func(net.Conn)) (Host, error) {
			<-resume // 挂起在开网卡途中，恢复时网卡与路由已经装好
			return host, nil
		})
	}()
	conn, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, make([]byte, preambleLen)); err != nil {
		t.Fatal(err)
	}
	ctl := &control{conn: conn}
	if err := ctl.send(message{Type: msgStart, Egress4: testEgress(t).IPv4.Index}); err != nil {
		t.Fatal(err)
	}
	conn.Close() // 主进程放弃等待并退出
	close(resume)
	if err := waitResult(t, result, 5*time.Second); err == nil {
		t.Error("主进程已不在时 helper 应以错误退出")
	}
	select {
	case <-host.closed:
	default:
		t.Error("helper 恢复后没有拆掉网卡")
	}
}
