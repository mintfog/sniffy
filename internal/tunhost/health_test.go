// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingLogger 记下 Logger 收到的日志。
type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) Warn(msg string, args ...any) { l.add("WARN " + fmt.Sprintf(msg, args...)) }

func (l *recordingLogger) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
}

func (l *recordingLogger) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

// waitLine 等到日志里出现含 substr 的一行。
func (l *recordingLogger) waitLine(t *testing.T, substr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, line := range l.list() {
			if strings.Contains(line, substr) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("日志里没有出现 %q: %q", substr, l.list())
}

func TestHandoffHealth(t *testing.T) {
	t.Parallel()
	var reports []string
	var gaveUp []error
	h := &handoffHealth{
		report: func(msg string) { reports = append(reports, msg) },
		giveUp: func(err error) { gaveUp = append(gaveUp, err) },
	}
	fail := errors.New("connect: no such file or directory")

	h.succeeded()
	if len(reports) != 0 {
		t.Fatalf("没有失败过时成功不应回报: %q", reports)
	}
	h.failed(fail)
	h.failed(fail)
	if len(reports) != 1 || !strings.Contains(reports[0], "no such file") {
		t.Fatalf("一轮连续失败应只在开始时回报一次原因: %q", reports)
	}
	h.succeeded()
	h.succeeded()
	if len(reports) != 2 || !strings.Contains(reports[1], "已恢复") || !strings.Contains(reports[1], "2 次") {
		t.Fatalf("一轮连续失败结束时应回报一次恢复与失败次数: %q", reports)
	}

	// 只失败一次、之后没有新连接，过多久都不放弃。
	h.failed(fail)
	for range 3 * handoffGiveUpTicks {
		h.tick()
	}
	if len(gaveUp) != 0 {
		t.Fatalf("只失败一次就放弃了: %v", gaveUp)
	}
	// 失败够多次、但还没过够心跳周期，不放弃：主进程可能正在重建通道。
	h.succeeded()
	for range 2 * handoffGiveUpFailures {
		h.failed(fail)
	}
	if len(gaveUp) != 0 {
		t.Fatalf("没过够 %d 个心跳周期就放弃了: %v", handoffGiveUpTicks, gaveUp)
	}
	for range handoffGiveUpTicks {
		h.tick()
	}
	h.failed(fail)
	if len(gaveUp) != 1 || !errors.Is(gaveUp[0], fail) || !strings.Contains(gaveUp[0].Error(), "持续失败") {
		t.Fatalf("失败持续时应放弃并带上最近的原因: %v", gaveUp)
	}
}

// controlPair 返回一对相连的控制连接。
func controlPair(t *testing.T) (a, b *control) {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath(t, "ch"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		c, _ := ln.AcceptUnix()
		accepted <- c
	}()
	dialed, err := net.DialUnix("unix", nil, ln.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	other := <-accepted
	t.Cleanup(func() { dialed.Close(); other.Close() })
	return &control{conn: dialed}, &control{conn: other}
}

// recvAll 在后台不设期限地读 c，读到的消息与最后的错误送进返回的通道。
func recvAll(c *control) (<-chan message, <-chan error) {
	msgs, errs := make(chan message, 64), make(chan error, 1)
	go func() {
		for {
			m, err := c.recv(0)
			if err != nil {
				errs <- err
				return
			}
			msgs <- m
		}
	}()
	return msgs, errs
}

// TestHeartbeatCountsPeriods：失联按心跳周期计数。连续 heartbeatMisses 个周期收不到对方的消息即
// 让 recv 以超时返回；周期之间隔得再久（系统休眠，计时器唤醒后只补一次）也不算失联。
func TestHeartbeatCountsPeriods(t *testing.T) {
	t.Parallel()
	local, peer := controlPair(t)
	peerMsgs, _ := recvAll(peer)
	_, errs := recvAll(local)
	ticks, stop := make(chan time.Time), make(chan struct{})
	defer close(stop)
	go local.beat(ticks, stop, func() {})

	tick := func() {
		ticks <- time.Now()
		if m := <-peerMsgs; m.Type != msgPing {
			t.Fatalf("每个周期应发一次 ping，对方收到 %+v", m)
		}
	}
	for range heartbeatMisses - 1 {
		tick()
	}
	// 如同休眠：超过 heartbeatTimeout 没有任何周期，醒来后对方先发来消息。
	time.Sleep(heartbeatTimeout + time.Second)
	select {
	case err := <-errs:
		t.Fatalf("周期之间隔得久就判定了失联: %v", err)
	default:
	}
	if err := peer.send(message{Type: msgPing}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	tick()
	for range heartbeatMisses - 1 {
		tick()
	}
	select {
	case err := <-errs:
		t.Fatalf("对方的消息之后只过了 %d 个周期就判定了失联: %v", heartbeatMisses-1, err)
	case <-time.After(50 * time.Millisecond):
	}
	ticks <- time.Now()
	select {
	case err := <-errs:
		if !isTimeout(err) {
			t.Errorf("失联时 recv 返回 %v，应为超时", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("连续 %d 个周期没有消息，没有判定失联", heartbeatMisses)
	}
}

// TestHeartbeatKeepsWatchingAfterSendFails：本端关了写端、ping 发不出去之后仍要发现对方失联：主进程
// 关闭时就是这样等 helper 收尾的。
func TestHeartbeatKeepsWatchingAfterSendFails(t *testing.T) {
	t.Parallel()
	local, _ := controlPair(t)
	_, errs := recvAll(local)
	_ = local.conn.CloseWrite()
	ticks, stop := make(chan time.Time), make(chan struct{})
	defer close(stop)
	go local.beat(ticks, stop, func() {})
	for range heartbeatMisses {
		ticks <- time.Now()
	}
	select {
	case err := <-errs:
		if !isTimeout(err) {
			t.Errorf("recv 返回 %v，应为超时", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("发不出 ping 之后不再判断对方是否失联")
	}
}
