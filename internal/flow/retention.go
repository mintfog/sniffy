// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

// 长连接会话(WebSocket / SSE / gRPC / 分块流)时间线的保留策略,四条记录路径共用:
// 抓包侧的 wsRecorder 与 streamRecorder,构造器侧的 composeWSConn 与 composeSSERun。
//
// 三道闸门缺一不可 —— 只按条数封顶时,500 条大帧照样能让单会话占到 GiB 级,
// 而会话存储本身能装 2000 条会话(service.newWSStore),乘起来根本没有上界:
//
//   - MaxRetainedMessageBytes:单条只留前 N 字节。
//   - MaxSessionBytes:一条会话时间线的总字节预算。
//   - MaxWSMessages / MaxStreamMessages:条数上限,挡住海量小消息堆爆条数。
//
// 三者都只裁剪展示用的时间线,MessageCount / TotalSize 继续按真实值累计。

// MaxRetainedMessageBytes 是单条消息在时间线里保留的载荷字节上限。
//
// 取 1 MiB 是因为 DTO 本来就按同样的尺度做预览截断(service 的 bodyPreviewLimit),
// 而 Messages 除了喂给那两个 DTO 构造函数之外没有别的消费者 —— 超出的字节永远到不了
// 界面,留着纯占内存。被裁剪的消息以 WSMessage.Size / StreamMessage.Size 保留真实长度。
const MaxRetainedMessageBytes = 1 << 20

// MaxSessionBytes 是单条会话时间线的字节预算。超出即按时间顺序淘汰最旧的消息,
// 直到重回预算内(最新的一条永远保留)。
const MaxSessionBytes = 8 << 20

// RetainPayload 返回一条消息实际存进时间线的载荷副本,以及它裁剪前的真实字节数。
//
// 必须复制:调用方手里的 data 多半是复用的读缓冲,直接存进会话会在下一帧被覆写。
func RetainPayload(data []byte) ([]byte, int64) {
	size := int64(len(data))
	if len(data) > MaxRetainedMessageBytes {
		data = data[:MaxRetainedMessageBytes]
	}
	return append([]byte(nil), data...), size
}

// TrimWSMessages 按条数与字节预算裁掉最旧的消息,返回裁剪后的切片。
func TrimWSMessages(msgs []WSMessage) []WSMessage {
	return trimTimeline(msgs, MaxWSMessages, func(m WSMessage) int { return len(m.Data) })
}

// TrimStreamMessages 按条数与字节预算裁掉最旧的消息,返回裁剪后的切片。
func TrimStreamMessages(msgs []StreamMessage) []StreamMessage {
	return trimTimeline(msgs, MaxStreamMessages, func(m StreamMessage) int { return len(m.Data) })
}

// trimTimeline 先按条数、再按字节预算算出要从头部丢弃多少条,然后就地前移。
//
// 至少保留最新的一条:单条即超预算时丢光了反而让界面上凭空少一帧,
// 而 MaxRetainedMessageBytes 已经保证了单条不会大到有意义的程度。
func trimTimeline[T any](msgs []T, maxCount int, sizeOf func(T) int) []T {
	cut := 0
	if len(msgs) > maxCount {
		cut = len(msgs) - maxCount
	}
	total := 0
	for i := cut; i < len(msgs); i++ {
		total += sizeOf(msgs[i])
	}
	for cut < len(msgs)-1 && total > MaxSessionBytes {
		total -= sizeOf(msgs[cut])
		cut++
	}
	return dropHead(msgs, cut)
}

// dropHead 丢弃前 cut 条并把剩下的前移。
//
// 腾出来的尾部槽位必须清零:它们还在底层数组的 cap 之内,残留的结构体会一直
// 攥着已淘汰消息的 Data 指针,GC 回收不掉 —— 那正是这套裁剪要解决的问题。
func dropHead[T any](msgs []T, cut int) []T {
	if cut <= 0 {
		return msgs
	}
	n := copy(msgs, msgs[cut:])
	var zero T
	for i := n; i < len(msgs); i++ {
		msgs[i] = zero
	}
	return msgs[:n]
}
