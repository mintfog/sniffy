// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/platform"
	"github.com/mintfog/sniffy/internal/tun"
)

// Session 是主进程这一侧的 helper：helper 开启的虚拟网卡上的每条 TCP 连接经交接连接交给
// handler。
type Session struct {
	ln   *net.UnixListener
	dir  string
	ctl  *control
	name string
	// helperPID 是控制连接对端的 PID，交接连接只接受来自它的。
	helperPID int
	handler   func(net.Conn)
	// tunPresent 报告虚拟网卡是否还在，见 tunPresent。
	tunPresent func() bool
	// startSent 表示已向 helper 下发 start：此后它可能已经装好网卡与路由。
	startSent bool

	closeOnce sync.Once
	closing   atomic.Bool
	err       error
	done      chan struct{}
}

// Launch 以管理员身份运行 helper 的命令行 cmd，不必等它结束。得知 helper 已经结束时调用 exited，
// 带上结束的原因（正常退出为 nil），可以在 Launch 返回之后、从任意 goroutine 调用；无从得知的
// （如由用户另行运行）不调用。Elevate 是自动提权的实现。
type Launch func(cmd []string, exited func(error)) error

// StartHelper 经 helper 子进程开启虚拟网卡，本进程不需要特权。
//
// 它先建好只有当前用户与提权后的 helper 能访问的通道，再把 helper 的命令行交给 launch，由
// launch 以管理员身份运行它；ctx 结束前 helper 没有连入即放弃，helper 先行结束的同样放弃。
// helper 连入后按 egress 与 opts 开启网卡，两者的要求同 StartLocal。每条 TCP 连接交给 handler，
// 连接的 RemoteAddr 是应用的原始源地址，LocalAddr 是原始目标地址，由 handler 负责关闭。
//
// 返回错误时会话通常为 nil。helper 收到开启指令后才失败、且 stopTimeout 内没能确认网卡已经
// 消失时，会话与错误一起返回：路由可能还在，调用方要像 Close 超时一样保持出站绑定，等 Done 关闭。
func StartHelper(ctx context.Context, egress netbind.Egress, opts Options, handler func(net.Conn), launch Launch) (*Session, error) {
	return startHelper(ctx, egress, opts, handler, launch, tunPresent)
}

// startHelper 即 StartHelper，present 判断虚拟网卡是否还在。
func startHelper(ctx context.Context, egress netbind.Egress, opts Options, handler func(net.Conn), launch Launch,
	present func() bool) (*Session, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("定位可执行文件: %w", err)
	}
	// 要在提权确认之前发现：按路径拉起的 helper 运行的是替换后的文件，会拒绝本进程。
	if err := checkRunningImage(exe); err != nil {
		return nil, err
	}
	// helper 的工作目录与本进程不同（pkexec 会切换），临时目录是相对路径时两端会解析到不同位置。
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return nil, fmt.Errorf("定位临时目录: %w", err)
	}
	dir, err := os.MkdirTemp(tmp, "sniffy-tun-")
	if err != nil {
		return nil, fmt.Errorf("创建通道目录: %w", err)
	}
	// Windows 上新目录继承 TEMP 的权限，TEMP 指向共享目录时别的用户也能连入通道、改动其中的
	// socket。只授权当前用户与提权后的 helper，后者可能属于另一个管理员账户。
	if err := platform.RestrictToOwnerAndAdmins(dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("收紧通道目录权限: %w", err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "ch"), Net: "unix"})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("创建通道: %w", err)
	}
	s := &Session{ln: ln, dir: dir, handler: handler, tunPresent: present, done: make(chan struct{})}
	cmd := []string{exe, Command, "--endpoint", ln.Addr().String(), "--pid", strconv.Itoa(os.Getpid())}
	start := message{Type: msgStart, Egress4: ifaceIndex(egress.IPv4), Egress6: ifaceIndex(egress.IPv6),
		BlockQUIC: opts.BlockQUIC}
	if err := s.connect(ctx, start, cmd, launch); err != nil {
		return s.abortStart(err)
	}
	go s.monitor()
	go s.acceptHandoffs()
	return s, nil
}

// abortStart 收拾开启失败的会话。已经下发 start 的，helper 可能装好了网卡与路由后才失去响应，
// 关闭控制连接让它拆掉，再等网卡消失，等不到就把会话一起返回，见 StartHelper。helper 无论在
// 装网卡之前还是之后挂起，恢复运行后都会因控制连接已关闭而拆掉网卡退出，不依赖本进程还在。
func (s *Session) abortStart(err error) (*Session, error) {
	if s.ctl != nil {
		_ = s.ctl.conn.Close()
	}
	s.release()
	if !s.startSent {
		return nil, err
	}
	s.closing.Store(true)
	go s.finish(nil)
	select {
	case <-s.done:
		return nil, err
	case <-time.After(stopTimeout):
		return s, fmt.Errorf("%w（helper 可能已装好虚拟网卡，它恢复运行后见通道已关闭会自行拆除）", err)
	}
}

// connect 运行 helper、等它连入后下发 start，直到它回报开启结果。
func (s *Session) connect(ctx context.Context, start message, cmd []string, launch Launch) error {
	// launch 会弹出提权确认或打印 helper 命令，已经放弃的就不再打扰用户。
	if err := context.Cause(ctx); err != nil {
		return fmt.Errorf("启动 helper: %w", err)
	}
	// 退出通知只用来结束等 helper 连入这一段。连入之后以控制连接上的报告为准：通知可能抢在报告
	// 之前到达，而 Windows 上通知里只有退出码。
	acceptCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	exited := func(err error) {
		cause := errExitedEarly
		if err != nil {
			cause = fmt.Errorf("%w: %w", errExitedEarly, err)
		}
		cancel(cause)
	}
	if err := launch(cmd, exited); err != nil {
		return fmt.Errorf("启动 helper: %w", err)
	}
	conn, pid, err := s.acceptControl(acceptCtx)
	if err != nil {
		return err
	}
	s.ctl = &control{conn: conn}
	s.helperPID = pid
	s.startSent = true
	if err := s.ctl.send(start); err != nil {
		return fmt.Errorf("向 helper 下发参数: %w", err)
	}
	// 开启网卡可能要等到 startTimeout。期间 ctx 结束（如用户中断）时半关闭控制连接，与 Close 一样要
	// helper 收尾，但仍等它回报开启结果再放弃：此前网卡可能还没出现，abortStart 会误以为它已经
	// 消失，路由却在本进程放弃之后才装上。
	stop := context.AfterFunc(ctx, func() { _ = s.ctl.conn.CloseWrite() })
	m, err := s.ctl.recv(startTimeout)
	if !stop() {
		return fmt.Errorf("等待 helper 开启网卡: %w", context.Cause(ctx))
	}
	if err != nil {
		return fmt.Errorf("等待 helper 开启网卡: %w", err)
	}
	switch m.Type {
	case msgStarted:
		s.name = m.Name
		return nil
	case msgError:
		return fmt.Errorf("helper: %s", m.Error)
	}
	return fmt.Errorf("helper 发来意外的消息 %q", m.Type)
}

func ifaceIndex(iface *net.Interface) int {
	if iface == nil {
		return 0
	}
	return iface.Index
}

// acceptControl 等 helper 连入控制连接，直到 ctx 结束，返回连接与 helper 的 PID。helper 先行结束时
// ctx 以 errExitedEarly 结束。能连上通道的只有当前用户与 root / 管理员，第一条连入的就是 helper；
// 它没通过核对即返回带原因的错误，被拒的 helper 随即退出，不会再连。
func (s *Session) acceptControl(ctx context.Context) (*net.UnixConn, int, error) {
	stop := context.AfterFunc(ctx, func() { _ = s.ln.SetDeadline(time.Now()) })
	defer stop()
	conn, err := s.ln.AcceptUnix()
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, fmt.Errorf("等待 helper 连入: %w", context.Cause(ctx))
		}
		return nil, 0, fmt.Errorf("等待 helper 连入: %w", err)
	}
	pid, err := admitControl(conn)
	if err != nil {
		_ = conn.Close()
		if errors.Is(err, errHungUp) {
			if cause := awaitExitReport(ctx); cause != nil {
				return nil, 0, fmt.Errorf("等待 helper 连入: %w", cause)
			}
		}
		return nil, 0, fmt.Errorf("拒绝连入的进程: %w", err)
	}
	// 回调已经开始执行的话，监听器的期限可能在此之后才被设上，后面就收不了交接连接。
	if !stop() {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("等待 helper 连入: %w", context.Cause(ctx))
	}
	return conn, pid, nil
}

var (
	errHungUp = errors.New("没完成握手就断开了")
	// errExitedEarly 是 launch 报告 helper 结束时 acceptControl 的 ctx 的结束原因。
	errExitedEarly = errors.New("helper 没有连入就结束了")
)

// awaitExitReport 在连入方没完成握手就断开后，等 launch 报告 helper 的退出原因，至多
// exitReportWait；等不到时返回 nil。helper 拒绝连接后随即退出，launch 报告的原因比「断开了」
// 具体：Linux、macOS 上自动提权时含 helper 的输出，Windows 上只有退出码。
func awaitExitReport(ctx context.Context) error {
	select {
	case <-ctx.Done():
		if cause := context.Cause(ctx); errors.Is(cause, errExitedEarly) {
			return cause
		}
	case <-time.After(exitReportWait):
	}
	return nil
}

const exitReportWait = time.Second

// admitControl 核对连入方运行着与本进程相同的可执行文件、且发来的是控制连接，返回它的 PID。
//
// 本进程无权查看的连入方照样接受：提权后的 helper 正是如此（Linux 上普通权限进程读不了 root 进程的
// /proc/<pid>/exe，Windows 上打不开另一个管理员账户的进程），拒绝它们就无法开启。因此这里只挡得住
// 本进程查看得了的其它程序。
func admitControl(conn *net.UnixConn) (int, error) {
	pid, err := peerProcess(conn)
	if err != nil {
		return 0, err
	}
	if err := checkSameExecutable(pid); err != nil && !errors.Is(err, fs.ErrPermission) {
		return 0, err
	}
	b := make([]byte, preambleLen)
	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	if _, err := io.ReadFull(conn, b); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("进程 %d %w（helper 拒绝连接时如此，手动运行 helper 时原因见它的输出）", pid, errHungUp)
		}
		return 0, fmt.Errorf("进程 %d: %w", pid, err)
	}
	if err := checkPreamble(b, kindControl); err != nil {
		return 0, fmt.Errorf("进程 %d: %w", pid, err)
	}
	return pid, nil
}

// acceptHandoffs 接收交接连接，直到 monitor 关闭监听器。
func (s *Session) acceptHandoffs() {
	for {
		conn, err := s.ln.AcceptUnix()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			// 文件描述符耗尽等，稍后重试。
			time.Sleep(10 * time.Millisecond)
			continue
		}
		go s.serveHandoff(conn)
	}
}

func (s *Session) serveHandoff(conn *net.UnixConn) {
	if pid, err := peerProcess(conn); err != nil || pid != s.helperPID {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	src, dst, err := readHandoff(conn)
	if err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	s.handler(&handoffConn{UnixConn: conn, src: net.TCPAddrFromAddrPort(src), dst: net.TCPAddrFromAddrPort(dst)})
}

// handoffConn 是交接过来的一条应用连接，地址与本进程直接从虚拟网卡收下的连接一致：
// RemoteAddr 是应用的原始源地址，LocalAddr 是原始目标地址。
type handoffConn struct {
	*net.UnixConn
	src, dst *net.TCPAddr
}

func (c *handoffConn) RemoteAddr() net.Addr { return c.src }
func (c *handoffConn) LocalAddr() net.Addr  { return c.dst }

// monitor 维持心跳并等控制连接结束，然后停止接收交接连接、清理通道，再等虚拟网卡消失。
// 已经交接过来的连接不受影响，helper 退出时它们随之结束。
//
// helper 失去响应时可能还持有网卡与路由，此时 Done 不能关闭：调用方会据此撤销出站绑定，
// sniffy 自己的出站就会进入无人处理的虚拟网卡。
func (s *Session) monitor() {
	stop := make(chan struct{})
	go s.ctl.heartbeat(stop)
	err := s.watchControl()
	close(stop)
	_ = s.ctl.conn.Close()
	s.release()
	if s.closing.Load() {
		err = nil
	}
	s.finish(err)
}

// finish 等虚拟网卡消失后记下 helper 停止的原因并关闭 done。
func (s *Session) finish(err error) {
	for s.tunPresent() {
		time.Sleep(nicPollInterval)
	}
	s.err = err
	close(s.done)
}

const (
	nicPollInterval = 200 * time.Millisecond
	// stopTimeout 是 Close 等 helper 拆掉网卡的时限，包含发现 helper 失去响应所需的
	// heartbeatTimeout。
	stopTimeout = 10 * time.Second
)

// tunPresent 报告系统里是否还有网卡带着虚拟网卡的地址。路由绑在网卡上，网卡消失即路由消失。
// 按地址而不是名字查找，不依赖各平台对网卡的命名。枚举失败时无从确认，按仍在处理：过早撤销
// 出站绑定会让 sniffy 自己的出站进入无人处理的虚拟网卡，多等则无害——绑定只作用于会进虚拟
// 网卡的连接。
func tunPresent() bool {
	return tunPresentIn(net.InterfaceAddrs)
}

func tunPresentIn(interfaceAddrs func() ([]net.Addr, error)) bool {
	addrs, err := interfaceAddrs()
	if err != nil {
		return true
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		addr, ok := netip.AddrFromSlice(ipnet.IP)
		if !ok {
			continue
		}
		for _, p := range tun.Prefixes() {
			if p.Addr() == addr.Unmap() {
				return true
			}
		}
	}
	return false
}

// watchControl 读控制消息直到控制连接结束，返回 helper 停止的原因。
func (s *Session) watchControl() error {
	var reported error
	stopped := false
	for {
		m, err := s.ctl.recv(heartbeatTimeout)
		switch {
		case err == nil:
			switch m.Type {
			case msgError:
				reported = fmt.Errorf("helper: %s", m.Error)
			case msgStopped:
				stopped = true
			}
		case reported != nil:
			return reported
		case stopped && errors.Is(err, io.EOF):
			return nil
		case errors.Is(err, io.EOF):
			return errors.New("helper 已退出")
		case isTimeout(err):
			return errors.New("helper 失去响应")
		default:
			return fmt.Errorf("控制连接中断: %w", err)
		}
	}
}

func (s *Session) release() {
	_ = s.ln.Close()
	_ = os.RemoveAll(s.dir)
}

// Name 返回虚拟网卡在系统里的名字。
func (s *Session) Name() string { return s.name }

// Done 在 helper 停止、且虚拟网卡已经消失后关闭。helper 失去响应而网卡还在时一直不关闭。
func (s *Session) Done() <-chan struct{} { return s.done }

// Err 返回 helper 自己停止的原因，在 Done 关闭后调用。经 Close 停止的、helper 自行正常停止的
// （收到退出信号）为 nil。
func (s *Session) Err() error { return s.err }

// Close 让 helper 拆掉网卡并退出，等到网卡消失后返回。helper 在 stopTimeout 内没有做到
// （失去响应）时返回错误，此时路由可能还在，Done 关闭才说明它们已经消失。
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.closing.Store(true)
		_ = s.ctl.conn.CloseWrite()
	})
	select {
	case <-s.done:
		return nil
	case <-time.After(stopTimeout):
		return fmt.Errorf("helper 没有在 %v 内拆掉虚拟网卡 %s", stopTimeout, s.name)
	}
}
