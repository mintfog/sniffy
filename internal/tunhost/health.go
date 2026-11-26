// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// 交接连接持续失败到什么程度就放弃：一轮连续失败（期间没有一次成功）至少 handoffGiveUpFailures
// 次，且从第一次失败起已过 handoffGiveUpTicks 个心跳周期。全机新建的 TCP 连接都要经交接连接，
// 它一直失败就等于断网，不如拆掉网卡恢复直连。只失败一两次、之后没有新连接的不算，可能只是一时
// 的资源紧张；通道文件被删时主进程在一个心跳周期内就地重建，期间的失败走不到这一步。
const (
	handoffGiveUpFailures = 3
	handoffGiveUpTicks    = 10
)

// handoffHealth 跟踪一端交接连接的连续失败。一轮连续失败开始与结束时各经 report 回报一次，期间的
// 失败不逐条回报；失败持续到上面的程度后，此后每次失败都调用 giveUp，重复的调用由 giveUp 忽略。
// 时长按心跳周期（tick）计数，理由同 control.heartbeat。
type handoffHealth struct {
	report func(msg string)
	giveUp func(err error)

	// failing 是 failures > 0 的无锁副本：交接成功是每条连接都走的路径，平时不必取锁。
	failing  atomic.Bool
	mu       sync.Mutex
	failures int
	ticks    int
}

func (h *handoffHealth) failed(err error) {
	h.mu.Lock()
	h.failures++
	h.failing.Store(true)
	n := h.failures
	persistent := n >= handoffGiveUpFailures && h.ticks >= handoffGiveUpTicks
	h.mu.Unlock()
	if n == 1 {
		h.report(fmt.Sprintf("交接连接失败，应用的这条连接被断开（此后连续的失败不再逐条回报）: %v", err))
	}
	if persistent {
		h.giveUp(fmt.Errorf("交接连接持续失败（连续 %d 次，最近一次: %w）", n, err))
	}
}

// succeeded 记下一次交接成功，结束正在进行的一轮连续失败。
func (h *handoffHealth) succeeded() {
	if !h.failing.Load() {
		return
	}
	h.mu.Lock()
	n := h.failures
	h.failures, h.ticks = 0, 0
	h.failing.Store(false)
	h.mu.Unlock()
	// 另一条连接的成功可能刚刚结束了这一轮。
	if n > 0 {
		h.report(fmt.Sprintf("交接连接已恢复（此前连续失败 %d 次）", n))
	}
}

// tick 在每个心跳周期调用一次。
func (h *handoffHealth) tick() {
	if !h.failing.Load() {
		return
	}
	h.mu.Lock()
	if h.failures > 0 {
		h.ticks++
	}
	h.mu.Unlock()
}
