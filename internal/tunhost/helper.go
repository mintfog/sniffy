// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
)

// Command 是 helper 的子命令名：<sniffy 可执行文件> tun-helper --endpoint <通道地址> --pid <主进程 PID>。
const Command = "tun-helper"

// RunIfRequested 在命令行是 helper 子命令时运行 helper，结束后退出进程；否则立即返回。
//
// 每个入口都要在 main 的最开头调用它，尤其要早于 Wails 初始化：否则单实例检测会把 helper
// 当成第二个实例，激活主窗口后直接退出。
func RunIfRequested() {
	if len(os.Args) < 2 || os.Args[1] != Command {
		return
	}
	if err := runHelper(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", Command, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runHelper(args []string) error {
	flags := flag.NewFlagSet(Command, flag.ContinueOnError)
	endpoint := flags.String("endpoint", "", "主进程的通道地址")
	mainPID := flags.Int("pid", 0, "拉起 helper 的主进程的 PID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *endpoint == "" || *mainPID <= 0 || flags.NArg() > 0 {
		return errors.New("用法: " + Command + " --endpoint <通道地址> --pid <主进程 PID>")
	}
	// helper 的存活堆很小，默认 GOGC 下每条连接的缓冲分配都会频繁触发 GC，新建连接速率被压到
	// 一半左右。
	debug.SetGCPercent(400)
	return serveHelper(*endpoint, *mainPID, StartLocal)
}

// serveHelper 连上主进程，按它的指令开启网卡并把 TCP 连接逐条交接过去，直到主进程要求关闭、
// 与主进程失联、网卡失效或通道被别的进程接管。返回前先拆掉网卡、再关闭控制连接。通道对端必须是
// 进程 mainPID，且运行着与本进程相同的可执行文件，见 peer.go。
func serveHelper(endpoint string, mainPID int, start func(netbind.Egress, func(net.Conn)) (Host, error)) error {
	raw, err := net.DialTimeout("unix", endpoint, handshakeTimeout)
	if err != nil {
		return fmt.Errorf("连接主进程: %w", err)
	}
	defer raw.Close()
	ctl := &control{conn: raw.(*net.UnixConn)}
	if err := verifyMain(ctl.conn, mainPID); err != nil {
		return err
	}
	_ = raw.SetWriteDeadline(time.Now().Add(handshakeTimeout))
	if _, err := raw.Write(preamble(kindControl)); err != nil {
		return fmt.Errorf("连接主进程: %w", err)
	}
	m, err := ctl.recv(handshakeTimeout)
	if err != nil {
		return fmt.Errorf("等待开启指令: %w", err)
	}
	if m.Type != msgStart {
		return fmt.Errorf("主进程发来意外的消息 %q", m.Type)
	}

	hijacked := make(chan error, 1)
	host, err := startFromMessage(m, start, handoff(endpoint, mainPID, hijacked))
	if err != nil {
		_ = ctl.send(message{Type: msgError, Error: err.Error()})
		return err
	}
	defer host.Close()
	if err := ctl.send(message{Type: msgStarted, Name: host.Name()}); err != nil {
		return fmt.Errorf("回报主进程: %w", err)
	}

	stop := make(chan struct{})
	defer close(stop)
	go ctl.heartbeat(stop)
	recvErr := make(chan error, 1)
	go func() {
		for {
			if _, err := ctl.recv(heartbeatTimeout); err != nil {
				recvErr <- err
				return
			}
		}
	}()

	select {
	case err := <-recvErr:
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("与主进程失联: %w", err)
	case <-host.Done():
		_ = ctl.send(message{Type: msgError, Error: "虚拟网卡已失效"})
		return errors.New("虚拟网卡已失效")
	case err := <-hijacked:
		_ = ctl.send(message{Type: msgError, Error: err.Error()})
		return err
	}
}

// verifyMain 确认通道对端是进程 mainPID，且运行着与本进程相同的可执行文件。
func verifyMain(conn *net.UnixConn, mainPID int) error {
	pid, err := peerProcess(conn)
	if err != nil {
		return err
	}
	if pid != mainPID {
		return fmt.Errorf("通道对端是进程 %d，不是拉起 helper 的主进程 %d，拒绝连接", pid, mainPID)
	}
	if err := checkSameExecutable(pid); err != nil {
		return fmt.Errorf("通道对端不是 sniffy，拒绝连接（sniffy 在主进程启动后被更新或替换时也会如此，重启 sniffy 即可）: %w", err)
	}
	return nil
}

// startFromMessage 按 start 消息开启网卡，连接交给 handler。消息只能指定出口网卡，其余参数固定。
func startFromMessage(m message, start func(netbind.Egress, func(net.Conn)) (Host, error), handler func(net.Conn)) (Host, error) {
	if m.Egress4 == 0 {
		return nil, errors.New("没有 IPv4 出口网卡")
	}
	var egress netbind.Egress
	var err error
	if egress.IPv4, err = net.InterfaceByIndex(m.Egress4); err != nil {
		return nil, fmt.Errorf("IPv4 出口网卡: %w", err)
	}
	if m.Egress6 != 0 {
		if egress.IPv6, err = net.InterfaceByIndex(m.Egress6); err != nil {
			return nil, fmt.Errorf("IPv6 出口网卡: %w", err)
		}
	}
	return start(egress, handler)
}

type halfConn interface {
	net.Conn
	CloseWrite() error
}

// handoff 返回虚拟网卡的连接回调：为每条连接向主进程建一条交接连接，再在两者之间中继。
//
// 每条交接连接都要重新核对对端是进程 mainPID：控制连接建立之后，通道所在的目录仍可能被换掉（临时
// 目录是共享的时候），之后的连接就会连到别的进程。发现时什么都还没写出，把原因送进 hijacked，由
// helper 拆掉网卡退出。
func handoff(endpoint string, mainPID int, hijacked chan<- error) func(net.Conn) {
	return func(conn net.Conn) {
		defer conn.Close()
		app, ok := conn.(halfConn)
		if !ok {
			return
		}
		src, srcOK := conn.RemoteAddr().(*net.TCPAddr)
		dst, dstOK := conn.LocalAddr().(*net.TCPAddr)
		if !srcOK || !dstOK {
			return
		}
		raw, err := net.DialTimeout("unix", endpoint, handshakeTimeout)
		if err != nil {
			return
		}
		defer raw.Close()
		ch := raw.(*net.UnixConn)
		pid, err := peerProcess(ch)
		if err != nil {
			return
		}
		if pid != mainPID {
			select {
			case hijacked <- fmt.Errorf("交接通道的对端是进程 %d，不是主进程 %d，通道可能已被别的进程接管", pid, mainPID):
			default:
			}
			return
		}
		if _, err := ch.Write(encodeHandoff(src.AddrPort(), dst.AddrPort())); err != nil {
			return
		}
		pipe(app, ch, appIdleTimeout)
	}
}

// appIdleTimeout 见 pipe。取值与主进程盲转发时对半关闭连接的空闲回收周期相同
// （capture/processors/http 的 halfClosedIdleTimeout）。
const appIdleTimeout = time.Minute

// pipeBufs 是中继复制用的缓冲，每块 64KB，与协议栈和网卡交换的最大分段一样大，一个大段
// 一次读写完。
var pipeBufs = sync.Pool{New: func() any {
	b := make([]byte, 64<<10)
	return &b
}}

// pipe 在应用连接与交接连接之间双向复制字节，两个方向都结束后返回，由调用方关闭两端。
//
// 连接何时结束由主进程决定，helper 不另设期限，只有一种情形例外：交接连接读到 EOF 时，主进程
// 可能只关了写端（源站关了写端，对向还要继续），也可能已经整个关闭，Unix socket 上两者读起来
// 一样。之后应用还发数据的，写交接连接失败即收尾；一直不发的，一个 idle 周期里没有数据即
// 断开。反过来应用关了写端时不设期限：主进程可能正停在断点上，或在等慢源站。
func pipe(app halfConn, ch *net.UnixConn, idle time.Duration) {
	var wg sync.WaitGroup
	wg.Go(func() { copyHalf(ch, app, idle) })
	wg.Go(func() {
		if copyHalf(app, ch, idle) {
			_ = app.SetReadDeadline(time.Now().Add(idle))
		}
	})
	wg.Wait()
}

// copyHalf 把 src 复制到 dst，读到 EOF 后把半关闭传给 dst 并返回 true。src 的读期限到点时，
// 这一轮有数据流过就再续一个 idle，一个字节都没有才算出错。出错时关闭两端以唤醒
// 对向阻塞中的复制，返回 false。
func copyHalf(dst, src halfConn, idle time.Duration) bool {
	buf := pipeBufs.Get().(*[]byte)
	defer pipeBufs.Put(buf)
	// 包一层以绕开 ReadFrom / WriteTo，保证复制用的是 buf。
	w, r := struct{ io.Writer }{dst}, struct{ io.Reader }{src}
	n, err := io.CopyBuffer(w, r, *buf)
	for n > 0 && isTimeout(err) {
		_ = src.SetReadDeadline(time.Now().Add(idle))
		n, err = io.CopyBuffer(w, r, *buf)
	}
	if err == nil {
		err = dst.CloseWrite()
	}
	if err != nil {
		_ = dst.Close()
		_ = src.Close()
		return false
	}
	return true
}
