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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
)

// handoffOnce 经 helper 交接一条应用连接，返回主进程是否收到了它。
func handoffOnce(t *testing.T, handler func(net.Conn), got <-chan net.Conn) bool {
	t.Helper()
	_, tunSide := tcpPair(t)
	go handler(tunSide)
	select {
	case conn := <-got:
		conn.Close()
		return true
	case <-time.After(time.Second):
		return false
	}
}

// waitRepaired 等通道文件重新出现。
func waitRepaired(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * heartbeatInterval)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("通道 %s 没有重建", path)
}

// TestChannelRepair：通道文件或整个目录被删（如临时目录的定期清理）后，主进程在原处重建，helper 的
// 交接连接随之恢复；期间失败的原因与恢复都由 helper 回报、记入主进程的日志。
func TestChannelRepair(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		remove func(s *Session) error
	}{
		{"通道文件", func(s *Session) error { return os.Remove(s.ch.path()) }},
		{"通道目录", func(s *Session) error { return os.RemoveAll(s.ch.dir) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testChannelRepair(t, tc.remove)
		})
	}
}

func testChannelRepair(t *testing.T, remove func(s *Session) error) {
	run, launch := launchFake(nil)
	got := make(chan net.Conn, 1)
	logger := &recordingLogger{}
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { got <- c }, launch, absent, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := <-run.handler
	if !handoffOnce(t, handler, got) {
		t.Fatal("通道完好时交接失败")
	}

	// 删除之后、交接之前可能恰好赶上主进程的检查，通道已经重建，再删一次。
	broken := false
	for range 4 {
		if err := remove(s); err != nil {
			t.Fatal(err)
		}
		if broken = !handoffOnce(t, handler, got); broken {
			break
		}
		waitRepaired(t, s.ch.path())
	}
	if !broken {
		t.Fatal("通道被删后交接仍然成功，用例没有起作用")
	}
	logger.waitLine(t, "helper: 交接连接失败", 2*time.Second)
	waitRepaired(t, s.ch.path())
	if !handoffOnce(t, handler, got) {
		t.Fatalf("通道重建后交接仍然失败: %q", logger.list())
	}
	logger.waitLine(t, "已在原处重建", time.Second)
	logger.waitLine(t, "helper: 交接连接已恢复（此前连续失败 1 次）", 2*time.Second)
	if info, err := os.Stat(s.ch.dir); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Errorf("重建的通道目录权限过宽: %v, %v", info, err)
	}
}

// TestHelperGivesUpWhenHandoffsKeepFailing：通道修不好、交接连接一直失败时，helper 拆掉网卡退出，
// 会话以失败的原因结束：全机新建的连接都失败等于断网，不如恢复直连。
func TestHelperGivesUpWhenHandoffsKeepFailing(t *testing.T) {
	t.Parallel()
	run, launch := launchFake(nil)
	logger := &recordingLogger{}
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launch, absent, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := <-run.handler
	// 原处换成一个普通文件，主进程无从重建。
	if err := os.RemoveAll(s.ch.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.ch.dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(s.ch.dir) })

	deadline := time.After(time.Duration(handoffGiveUpTicks+5) * heartbeatInterval)
	for {
		_, tunSide := tcpPair(t)
		go handler(tunSide)
		select {
		case <-s.Done():
		case <-time.After(500 * time.Millisecond):
			continue
		case <-deadline:
			t.Fatalf("交接连接一直失败，helper 没有放弃: %q", logger.list())
		}
		break
	}
	if s.Err() == nil || !strings.Contains(s.Err().Error(), "交接连接持续失败") {
		t.Errorf("会话 Err = %v，应带上交接连接持续失败的原因", s.Err())
	}
	select {
	case <-run.host.closed:
	default:
		t.Error("helper 放弃时没有拆掉网卡")
	}
	logger.waitLine(t, "重建失败", time.Second)
}

// TestSessionLogsOwnHandoffFailures：本进程这一侧收下交接连接失败时，原因同样记入日志。
func TestSessionLogsOwnHandoffFailures(t *testing.T) {
	t.Parallel()
	run, launch := launchFake(nil)
	got := make(chan net.Conn, 1)
	logger := &recordingLogger{}
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { got <- c }, launch, absent, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// helper 在本测试进程里运行，从这里连入通道的就是「helper」。
	bad, err := net.Dial("unix", s.ch.path())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = bad.Write([]byte("not a handoff header, but long enough to fill one....."))
	bad.Close()
	logger.waitLine(t, "交接连接失败", 2*time.Second)
	if !handoffOnce(t, <-run.handler, got) {
		t.Fatal("交接失败一次之后没有恢复")
	}
	logger.waitLine(t, "交接连接已恢复", time.Second)
}

// launchScripted 返回一个 launch：helper 完成握手、收到 start 后依次发出 replies，读到主进程关闭写端
// 后关闭连接。
func launchScripted(replies ...message) Launch {
	return func(cmd []string, _ func(error)) error {
		go func() {
			raw, err := net.Dial("unix", cmd[3])
			if err != nil {
				return
			}
			defer raw.Close()
			_, _ = raw.Write(preamble(kindControl))
			ctl := &control{conn: raw.(*net.UnixConn)}
			if _, err := ctl.recv(5 * time.Second); err != nil {
				return
			}
			for _, m := range replies {
				_ = ctl.send(m)
			}
			_, _ = io.Copy(io.Discard, raw)
		}()
		return nil
	}
}

// TestWarningBeforeStartResult：helper 装好路由后就开始交接连接，交接失败的 warning 可能抢在开启结果
// 之前到达，这时记入日志、接着等开启结果，不能当成开启失败，也不能顶掉真正的失败原因。
func TestWarningBeforeStartResult(t *testing.T) {
	t.Parallel()
	warning := message{Type: msgWarning, Error: "交接连接失败: connect: resource temporarily unavailable"}

	logger := &recordingLogger{}
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() },
		launchScripted(warning, message{Type: msgStarted, Name: "fake0"}), absent, logger)
	if err != nil {
		t.Fatalf("开启结果之前收到 warning 就开启失败: %v", err)
	}
	if s.Name() != "fake0" {
		t.Errorf("网卡名 %q，应为 helper 回报的 fake0", s.Name())
	}
	logger.waitLine(t, "helper: 交接连接失败", time.Second)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() },
		launchScripted(warning, message{Type: msgError, Error: "添加路由失败"}), absent, nil)
	if err == nil || !strings.Contains(err.Error(), "添加路由失败") {
		t.Errorf("开启失败的原因 = %v，应为 helper 回报的添加路由失败", err)
	}
}

// TestSetEgressReachesHelper：主进程下发的新出口原样到达 helper 的网卡；helper 更新失败的原因
// 记入主进程的日志。
func TestSetEgressReachesHelper(t *testing.T) {
	t.Parallel()
	ifaces, err := net.Interfaces()
	if err != nil || len(ifaces) == 0 {
		t.Skip("没有可用网卡", err)
	}
	run, launch := launchFake(nil)
	run.host.egressErr = errors.New("添加路由失败")
	logger := &recordingLogger{}
	s, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, launch, absent, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := netbind.Egress{IPv4: &ifaces[len(ifaces)-1], IPv6: &ifaces[0]}
	if err := s.SetEgress(want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-run.host.egress:
		if !netbind.SameInterface(got.IPv4, want.IPv4) || !netbind.SameInterface(got.IPv6, want.IPv6) {
			t.Errorf("helper 收到 %s，应为 %s", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("helper 没有收到新出口")
	}
	logger.waitLine(t, "helper: 更新出口网卡: 添加路由失败", 2*time.Second)

	if err := s.SetEgress(netbind.Egress{IPv4: want.IPv4}); err != nil {
		t.Fatal(err)
	}
	if got := <-run.host.egress; got.IPv6 != nil {
		t.Errorf("不接管 IPv6 时 helper 收到的 IPv6 出口 = %s，应为无", got.IPv6.Name)
	}
	select {
	case <-s.Done():
		t.Fatalf("更新出口失败不应结束会话: %v", s.Err())
	default:
	}
}
