// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

// newWindowActivation 返回非阻塞的唤醒请求函数，待窗口就绪后串行执行 activate。
// Wails 从 UI 线程向容量为 1 的队列投递单实例通知；若通知回调等待 UI 线程，会形成死锁。
// 独立 goroutine 执行窗口操作，并最多保留一次待处理请求；调用方关闭 done 时停止等待。
func newWindowActivation(windowReady, done <-chan struct{}, activate func()) func() {
	pending := make(chan struct{}, 1)
	go func() {
		select {
		case <-windowReady:
		case <-done:
			return
		}
		for {
			select {
			case <-pending:
			case <-done:
				return
			}
			// pending 与 done 可能同时就绪，执行前再次检查退出信号。
			select {
			case <-done:
				return
			default:
			}
			activate()
		}
	}()
	return func() {
		select {
		case pending <- struct{}{}:
		default:
		}
	}
}
