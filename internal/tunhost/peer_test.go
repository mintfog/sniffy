// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
)

// 冒充者是另起的测试进程，按环境变量扮演通道的一端：直接运行测试二进制是「同一个可执行文件的另一个
// 进程」，运行它的副本是「另一个可执行文件」。
const (
	peerModeEnv = "SNIFFY_TUNHOST_PEER"
	peerPathEnv = "SNIFFY_TUNHOST_PEER_PATH"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(peerModeEnv); mode != "" {
		os.Exit(runPeer(mode, os.Getenv(peerPathEnv)))
	}
	os.Exit(m.Run())
}

// runPeer 扮演冒充者。listen 在 path 上冒充主进程，每条连入的连接读到 EOF 后打印读到的字节数；
// control、handoff 连入 path 冒充 helper，发出控制连接或交接连接的开头，同样读到 EOF 后打印。
func runPeer(mode, path string) int {
	report := func(c net.Conn) {
		n, err := io.Copy(io.Discard, c)
		if err != nil {
			fmt.Printf("reset %d\n", n) // 对方没读完已发出的字节就关闭，收到 RST
			return
		}
		fmt.Printf("read %d\n", n)
	}
	if mode == "listen" {
		ln, err := net.Listen("unix", path)
		if err != nil {
			fmt.Println("error", err)
			return 1
		}
		fmt.Println("ready")
		for {
			c, err := ln.Accept()
			if err != nil {
				return 1
			}
			report(c)
			c.Close()
		}
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		fmt.Println("error", err)
		return 1
	}
	defer c.Close()
	head := preamble(kindControl)
	if mode == "handoff" {
		ap := netip.MustParseAddrPort("10.0.0.1:1234")
		head = encodeHandoff(ap, ap)
	}
	// 对方核对身份后不读就关闭连接，可能赶在这次写入之前（写入报 broken pipe），之后照样能读到
	// 连接已关闭，结果只看收到的字节数。
	_, _ = c.Write(head)
	report(c)
	return 0
}

// impostor 是一个冒充者进程，lines 收到它打印的每一行。
type impostor struct {
	pid   int
	lines chan string
}

// startImpostor 以 mode 运行 exe 扮演冒充者，测试结束时杀掉它。
func startImpostor(t *testing.T, exe, mode, path string) *impostor {
	t.Helper()
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), peerModeEnv+"="+mode, peerPathEnv+"="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	p := &impostor{pid: cmd.Process.Pid, lines: make(chan string, 16)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
		close(p.lines)
	}()
	if mode == "listen" {
		if line := p.next(t); line != "ready" {
			t.Fatalf("冒充者没能监听: %s", line)
		}
	}
	return p
}

func (p *impostor) next(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-p.lines:
		if !ok {
			t.Fatal("冒充者已退出")
		}
		return line
	case <-time.After(10 * time.Second):
		t.Fatal("冒充者没有在时限内输出")
		return ""
	}
}

// expectNothingSent 确认冒充者的连接被对方关闭，且没收到任何字节。
func (p *impostor) expectNothingSent(t *testing.T) {
	t.Helper()
	if line := p.next(t); line != "read 0" && line != "reset 0" {
		t.Errorf("冒充者 %s，应在一个字节都没收到时就被关闭", line)
	}
}

func testExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

// otherExecutable 返回测试二进制的副本：内容相同，但是另一个文件。
func otherExecutable(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(testExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	name := "impostor"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustNotStart(t *testing.T) startFunc {
	return func(netbind.Egress, Options, func(net.Conn)) (Host, error) {
		t.Error("helper 没有拒绝冒充者，就开启了网卡")
		return nil, fmt.Errorf("不应开启")
	}
}

func TestHelperRejectsOtherExecutable(t *testing.T) {
	t.Parallel()
	endpoint := filepath.Join(t.TempDir(), "ch")
	p := startImpostor(t, otherExecutable(t), "listen", endpoint)
	// 冒充者自己拉起 helper 时，命令行里的 PID 也由它决定。
	err := serveHelper(context.Background(), endpoint, p.pid, mustNotStart(t))
	if err == nil || !strings.Contains(err.Error(), "不是 sniffy") {
		t.Fatalf("helper 返回 %v，应拒绝另一个可执行文件", err)
	}
	p.expectNothingSent(t)
}

func TestHelperRejectsOtherMainProcess(t *testing.T) {
	t.Parallel()
	endpoint := filepath.Join(t.TempDir(), "ch")
	// 同一个可执行文件另起的主进程，通道被换成它的。
	p := startImpostor(t, testExecutable(t), "listen", endpoint)
	err := serveHelper(context.Background(), endpoint, os.Getpid(), mustNotStart(t))
	if err == nil || !strings.Contains(err.Error(), "不是拉起 helper 的主进程") {
		t.Fatalf("helper 返回 %v，应拒绝不是拉起它的主进程", err)
	}
	p.expectNothingSent(t)
}

func TestSessionRejectsImpostorHelper(t *testing.T) {
	t.Parallel()
	other := otherExecutable(t)
	var p *impostor
	began := time.Now()
	_, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(cmd []string, _ func(error)) error {
		p = startImpostor(t, other, "control", cmd[3])
		return nil
	}, absent, nil)
	if err == nil || !strings.Contains(err.Error(), "拒绝连入的进程") || !strings.Contains(err.Error(), "运行的不是本程序") {
		t.Fatalf("开启返回 %v，应拒绝另一个可执行文件冒充的 helper 并说明原因", err)
	}
	// 不等 ctx 结束：被拒之后不会再有 helper 连入。
	if d := time.Since(began); d > 5*time.Second {
		t.Errorf("拒绝后过了 %v 才返回，应立即报告", d)
	}
	p.expectNothingSent(t)
}

func TestSessionRejectsHandoffFromOtherProcess(t *testing.T) {
	t.Parallel()
	_, launch := launchFake(nil)
	got := make(chan net.Conn, 1)
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { got <- c }, launch, absent, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 同一个可执行文件，但不是连入控制连接的那个 helper。
	p := startImpostor(t, testExecutable(t), "handoff", s.ch.path())
	p.expectNothingSent(t)
	select {
	case c := <-got:
		c.Close()
		t.Fatal("主进程收下了别的进程发来的交接连接")
	default:
	}
}

func TestHelperStopsWhenChannelHijacked(t *testing.T) {
	t.Parallel()
	run, launch := launchFake(nil)
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launch, absent, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 控制连接建立之后，通道被换成同一个可执行文件另起的进程。
	endpoint := s.ch.path()
	if err := os.Remove(endpoint); err != nil {
		t.Fatal(err)
	}
	p := startImpostor(t, testExecutable(t), "listen", endpoint)

	_, tunSide := tcpPair(t)
	go (<-run.handler)(tunSide)
	p.expectNothingSent(t)
	err = waitResult(t, run.result, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "被别的进程接管") {
		t.Fatalf("helper 返回 %v，应因通道被接管而退出", err)
	}
	select {
	case <-run.host.closed:
	default:
		t.Error("helper 退出时没有拆掉网卡")
	}
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("helper 退出后会话没有结束")
	}
	if s.Err() == nil || !strings.Contains(s.Err().Error(), "被别的进程接管") {
		t.Errorf("会话的停止原因是 %v，应转述 helper 报告的通道被接管", s.Err())
	}
}

// BenchmarkPeerProcess 是每条交接连接在两端各多出的一次开销。
func BenchmarkPeerProcess(b *testing.B) {
	path := filepath.Join(b.TempDir(), "ch")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, _ := ln.AcceptUnix()
		defer c.Close()
		_, _ = io.Copy(io.Discard, c)
	}()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	for b.Loop() {
		if _, err := peerProcess(c); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCheckRunningImage(t *testing.T) {
	t.Parallel()
	if err := checkRunningImage(testExecutable(t)); err != nil {
		t.Errorf("本进程的可执行文件被判为已替换: %v", err)
	}
	// 内容相同的另一个文件：同路径替换（如升级或重新安装）后，路径指向的就是这样一个文件。
	if err := checkRunningImage(otherExecutable(t)); err == nil || !strings.Contains(err.Error(), "被替换") {
		t.Errorf("换过的文件返回 %v，应报告已被替换", err)
	}
}
