// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// busWait 是等待事件的上限:裸 <-ch 在投递回归时会把测试挂到包级超时(默认 10 分钟),
// 最终只能拿到一堆 goroutine dump 而非失败点,故所有收取都带超时。
const busWait = 2 * time.Second

func recvEvent(t *testing.T, name string, ch <-chan Event) Event {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatalf("%s 订阅者的 channel 已关闭", name)
		}
		return e
	case <-time.After(busWait):
		t.Fatalf("%s 订阅者在 %s 内没有收到事件", name, busWait)
		return Event{}
	}
}

// waitOrFatal 在 busWait 内等待 fn 返回。fn 跑在独立协程里,故它若卡死(取消等写锁、
// 等待发布协程收尾),失败点仍落在本用例上,而不是把整个包拖到测试超时。
func waitOrFatal(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(busWait):
		t.Fatalf("%s 在 %s 内未完成", what, busWait)
	}
}

func expectNoEvent(t *testing.T, name string, ch <-chan Event) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("%s 订阅者不应再收到事件,却收到 %#v", name, e)
	default:
	}
}

func TestEventBusFanoutAndCancel(t *testing.T) {
	bus := NewEventBus()
	if bus.bufferSize != defaultBusBuffer {
		t.Fatalf("默认缓冲大小 = %d，期望 %d", bus.bufferSize, defaultBusBuffer)
	}

	first, cancelFirst := bus.Subscribe()
	second, cancelSecond := bus.Subscribe()
	defer cancelSecond()
	if len(bus.subscribers) != 2 {
		t.Fatalf("订阅者数量 = %d，期望 2", len(bus.subscribers))
	}

	want := Event{Type: EventFlowCompleted, Payload: "flow-1"}
	bus.Publish(want)
	for _, sub := range []struct {
		name string
		ch   <-chan Event
	}{{"first", first}, {"second", second}} {
		if got := recvEvent(t, sub.name, sub.ch); got != want {
			t.Errorf("%s 订阅者收到 %#v，期望 %#v", sub.name, got, want)
		}
	}

	cancelFirst()
	cancelFirst() // 取消函数应当幂等:非幂等会在这里 close 一个已关闭的 channel 而 panic。
	if _, ok := <-first; ok {
		t.Fatal("取消订阅后 channel 应关闭")
	}
	if len(bus.subscribers) != 1 {
		t.Fatalf("取消一个订阅后仍有 %d 个订阅者，期望 1", len(bus.subscribers))
	}

	bus.Emit(EventStatsTick, 42)
	got := recvEvent(t, "second", second)
	if got.Type != EventStatsTick || got.Payload != 42 {
		t.Fatalf("Emit 收到 %#v，期望 stats_tick/42", got)
	}
}

// TestEventBusSlowSubscriberDoesNotStarveOthers 锁定扇出总线最关键的隔离性:
// 一个不消费的订阅者只丢自己的消息,不得影响其他订阅者的完整投递。
func TestEventBusSlowSubscriberDoesNotStarveOthers(t *testing.T) {
	const buffer, total = 2, 6
	bus := NewEventBus()
	bus.bufferSize = buffer

	// 取消订阅刻意不放进 defer:阻塞回归时发布方会持着读锁挂住,取消要等写锁,
	// 会和它死锁;订阅本身不起协程,不取消也没有泄漏,取消语义由扇出用例覆盖。
	slow, _ := bus.Subscribe()
	fast, _ := bus.Subscribe()

	// fast 每发一条就取走,故永不积压;slow 全程不消费。
	for i := 0; i < total; i++ {
		// 发布同样套超时:阻塞回归时 slow 的缓冲一满就把 Emit 挂在主协程上,
		// 裸调用会把失败点挪到包级超时。
		waitOrFatal(t, "发布事件", func() { bus.Emit(EventFlowUpdated, i) })
		if got := recvEvent(t, "fast", fast); got.Payload != i {
			t.Fatalf("fast 订阅者第 %d 条收到 %#v，期望 payload %d", i, got, i)
		}
	}

	// slow 只应留下最早的 buffer 条,其余在缓冲满时被丢弃。
	for i := 0; i < buffer; i++ {
		if got := recvEvent(t, "slow", slow); got.Payload != i {
			t.Fatalf("slow 订阅者第 %d 条收到 %#v，期望 payload %d", i, got, i)
		}
	}
	expectNoEvent(t, "slow", slow)
}

// TestEventBusPublishNeverBlocks 校验注释里那条硬约束:订阅者不消费时,
// 发布方(代理热路径)绝不能被拖住。
func TestEventBusPublishNeverBlocks(t *testing.T) {
	bus := NewEventBus()
	bus.bufferSize = 1
	_, cancel := bus.Subscribe() // 故意不消费

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			bus.Emit(EventFlowStarted, i)
		}
	}()

	select {
	case <-done:
		cancel()
	case <-time.After(busWait):
		// 取消刻意不放进 defer:阻塞回归时发布协程正持着读锁,cancel 要等写锁,
		// 会与它死锁,把这条本该 2s 出结果的失败拖成包级超时。
		t.Fatalf("订阅者不消费时 Publish 在 %s 内未完成，说明发布方被阻塞", busWait)
	}
}

func TestEventBusConcurrentPublishAndCancel(t *testing.T) {
	const publishers, perPublisher = 4, 2000

	bus := NewEventBus()
	bus.bufferSize = 1
	_, cancel := bus.Subscribe()

	var emitted atomic.Int64
	// 每个 publisher 发到一半就停在 gate 上,主协程放行后立即取消:这样取消时每个
	// publisher 都必然还剩一半没发,不会因调度(如 GOMAXPROCS=1)退化成「发完再取消」。
	var halfway, finished sync.WaitGroup
	halfway.Add(publishers)
	finished.Add(publishers)
	gate := make(chan struct{})
	for i := 0; i < publishers; i++ {
		go func() {
			defer finished.Done()
			for j := 0; j < perPublisher; j++ {
				if j == perPublisher/2 {
					halfway.Done()
					<-gate
				}
				bus.Emit(EventConnStarted, nil)
				emitted.Add(1)
			}
		}()
	}

	// 三处等待都套上超时:阻塞回归会让 publisher 卡在第一次 Emit 上(连一半都发不到),
	// 裸 Wait 会把失败点从这里挪到包级超时。
	waitOrFatal(t, "发布协程发到一半", halfway.Wait)
	close(gate)
	waitOrFatal(t, "并发发布期间取消订阅", func() {
		cancel()
		cancel() // 并发发布期间重复取消同样应幂等。
	})
	waitOrFatal(t, "取消订阅后发布协程收尾", finished.Wait)

	if got := emitted.Load(); got != publishers*perPublisher {
		t.Fatalf("实际发布 %d 条，期望 %d 条", got, publishers*perPublisher)
	}

	// 取消后继续发布也应安全且不阻塞。
	bus.Emit(EventConnEnded, nil)
}
