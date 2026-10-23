// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"testing"
	"testing/synctest"
)

func TestWindowActivationBeforeReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ready := make(chan struct{})
		done := make(chan struct{})
		defer close(done)
		calls := 0
		request := newWindowActivation(ready, done, func() { calls++ })

		requestActivationRepeatedly(t, request)
		if calls != 0 {
			t.Fatalf("窗口就绪前激活了 %d 次", calls)
		}
		close(ready)
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("窗口就绪后激活了 %d 次，期望合并为 1 次", calls)
		}
	})
}

func TestWindowActivationWhileShowing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ready := make(chan struct{})
		close(ready)
		done := make(chan struct{})
		defer close(done)
		release := make(chan struct{})
		defer close(release)
		calls := 0
		request := newWindowActivation(ready, done, func() {
			calls++
			// 模拟窗口操作同步等待 UI 线程。
			<-release
		})
		synctest.Wait()
		if calls != 0 {
			t.Fatal("未收到通知时不应激活窗口")
		}

		request()
		synctest.Wait()
		requestActivationRepeatedly(t, request)
		if calls != 1 {
			t.Fatalf("窗口操作阻塞时并发激活了 %d 次，期望仅 1 次", calls)
		}
		release <- struct{}{}
		synctest.Wait()
		if calls != 2 {
			t.Fatalf("首轮激活完成后执行了 %d 次，期望处理 1 次合并请求", calls)
		}
	})
}

func TestWindowActivationStopBeforeReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ready := make(chan struct{})
		done := make(chan struct{})
		calls := 0
		request := newWindowActivation(ready, done, func() { calls++ })
		request()
		close(done)
		synctest.Wait()
		close(ready)
		requestActivationRepeatedly(t, request)
		if calls != 0 {
			t.Fatalf("窗口就绪前退出后仍激活了 %d 次", calls)
		}
	})
}

func TestWindowActivationStopWhileShowing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ready := make(chan struct{})
		close(ready)
		done := make(chan struct{})
		release := make(chan struct{})
		calls := 0
		request := newWindowActivation(ready, done, func() {
			calls++
			<-release
		})

		request()
		synctest.Wait()
		request()
		close(done)
		close(release)
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("退出后仍执行了待处理请求，共激活 %d 次，期望 1 次", calls)
		}
	})
}

func requestActivationRepeatedly(t *testing.T, request func()) {
	t.Helper()
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		for range 3 {
			request()
		}
	}()
	synctest.Wait()
	select {
	case <-completed:
	default:
		t.Fatal("连续唤醒请求未返回")
	}
}
