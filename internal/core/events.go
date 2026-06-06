// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import "sync"

// EventType 是引擎向上层(service → transport)广播的事件类型。
type EventType string

const (
	EventFlowStarted        EventType = "flow_started"        // 读到请求,出现 pending flow
	EventFlowUpdated        EventType = "flow_updated"        // flow 被更新(如异步补进程信息)
	EventFlowCompleted      EventType = "flow_completed"      // flow 完成(含响应)
	EventBreakpointHit      EventType = "breakpoint_hit"      // 命中断点,等待 UI 放行
	EventBreakpointResolved EventType = "breakpoint_resolved" // 断点已放行/超时
	EventWSMessage          EventType = "ws_message"          // WebSocket 消息
	EventConnStarted        EventType = "conn_started"        //
	EventConnEnded          EventType = "conn_ended"          //
	EventStatsTick          EventType = "stats_tick"          // 周期统计快照
	EventPluginReloaded     EventType = "plugin_reloaded"     //
	EventPluginErrored      EventType = "plugin_errored"      //
)

// Event 是广播到事件总线上的一条消息。
type Event struct {
	Type    EventType `json:"type"`
	Payload any       `json:"payload,omitempty"`
}

// EventBus 是一个简单的扇出总线。
//
// 关键约束:发布永不阻塞代理热路径——订阅者 channel 满时直接丢弃该订阅者的
// 这条消息(慢消费者丢弃策略),由订阅者自行通过重新拉取对账。
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[int]chan Event
	nextID      int
	bufferSize  int
}

// NewEventBus 创建事件总线。bufferSize<=0 时使用默认缓冲。
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[int]chan Event),
		bufferSize:  256,
	}
}

// Subscribe 注册一个订阅者,返回事件 channel 与取消订阅函数。
func (b *EventBus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++
	ch := make(chan Event, b.bufferSize)
	b.subscribers[id] = ch

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			close(c)
		}
	}
	return ch, cancel
}

// Publish 向所有订阅者非阻塞地广播一条事件。
func (b *EventBus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default:
			// 慢消费者:丢弃,绝不阻塞发布方(代理热路径)。
		}
	}
}

// Emit 是 Publish 的便捷封装。
func (b *EventBus) Emit(t EventType, payload any) {
	b.Publish(Event{Type: t, Payload: payload})
}
