// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import "testing"

func TestRetainPayloadCapsAndReportsTrueSize(t *testing.T) {
	t.Parallel()
	big := make([]byte, MaxRetainedMessageBytes+1024)
	for i := range big {
		big[i] = byte(i)
	}

	got, size := RetainPayload(big)
	if size != int64(len(big)) {
		t.Fatalf("真实长度 = %d,期望 %d", size, len(big))
	}
	if len(got) != MaxRetainedMessageBytes {
		t.Fatalf("保留长度 = %d,期望 %d", len(got), MaxRetainedMessageBytes)
	}
	m := WSMessage{Data: got, Size: size}
	if !m.Truncated() || m.PayloadSize() != size {
		t.Fatalf("裁剪标记/真实长度不对: truncated=%v size=%d", m.Truncated(), m.PayloadSize())
	}

	// 必须是副本:调用方手上多半是复用的读缓冲,存进会话后被覆写就串味了。
	small := []byte("abc")
	kept, _ := RetainPayload(small)
	small[0] = 'z'
	if string(kept) != "abc" {
		t.Fatalf("保留的载荷应是副本,实际被上游改动为 %q", kept)
	}
}

func TestRetainPayloadKeepsShortMessagesIntact(t *testing.T) {
	t.Parallel()
	got, size := RetainPayload([]byte("hello"))
	if string(got) != "hello" || size != 5 {
		t.Fatalf("短消息不该被动:data=%q size=%d", got, size)
	}
	if (WSMessage{Data: got, Size: size}).Truncated() {
		t.Fatal("未超限的消息不该标记为已裁剪")
	}
	// Size 为零(未过保留策略的消息,如管道就地构造的那种)时回落到 len(Data)。
	if n := (WSMessage{Data: got}).PayloadSize(); n != 5 {
		t.Fatalf("Size 缺省时应回落到 len(Data),实际 %d", n)
	}
}

// 单条大小与条数都合规,但累计字节超预算 —— 这正是只按条数封顶时漏掉的那一档。
func TestTrimWSMessagesEnforcesByteBudget(t *testing.T) {
	t.Parallel()
	const each = MaxRetainedMessageBytes // 1 MiB/条,8 条即撞上 MaxSessionBytes
	var msgs []WSMessage
	for i := 0; i < 12; i++ {
		msgs = append(msgs, WSMessage{ID: string(rune('a' + i)), Data: make([]byte, each)})
	}
	got := TrimWSMessages(msgs)

	if len(got) > MaxWSMessages {
		t.Fatalf("条数 = %d,超过上限 %d", len(got), MaxWSMessages)
	}
	total := 0
	for _, m := range got {
		total += len(m.Data)
	}
	if total > MaxSessionBytes {
		t.Fatalf("保留字节 = %d,超过预算 %d", total, MaxSessionBytes)
	}
	// 淘汰的是最旧的,留下的必须是最新一批且保持时间顺序。
	if got[len(got)-1].ID != string(rune('a'+11)) {
		t.Fatalf("最后一条 = %q,期望最新的那条", got[len(got)-1].ID)
	}
	if got[0].ID >= got[len(got)-1].ID {
		t.Fatal("裁剪后顺序被打乱")
	}
}

// 被淘汰的槽位必须清零,否则它们还在 cap 之内攥着大 Data 的指针,GC 回收不掉 ——
// 那正是这套裁剪要解决的问题,只看 len 是测不出来的。
func TestTrimReleasesEvictedPayloads(t *testing.T) {
	t.Parallel()
	msgs := make([]WSMessage, 0, 16)
	for i := 0; i < 12; i++ {
		msgs = append(msgs, WSMessage{Data: make([]byte, MaxRetainedMessageBytes)})
	}
	got := TrimWSMessages(msgs)

	tail := got[:cap(got)]
	for i := len(got); i < len(tail); i++ {
		if tail[i].Data != nil {
			t.Fatalf("下标 %d 的已淘汰条目仍持有 Data,GC 回收不掉", i)
		}
	}
}

func TestTrimWSMessagesEnforcesCountCap(t *testing.T) {
	t.Parallel()
	msgs := make([]WSMessage, MaxWSMessages+7)
	for i := range msgs {
		msgs[i] = WSMessage{Data: []byte("x")} // 海量小消息:字节预算根本碰不到
	}
	if got := TrimWSMessages(msgs); len(got) != MaxWSMessages {
		t.Fatalf("条数 = %d,期望 %d", len(got), MaxWSMessages)
	}
}

// 单条即超预算时也要留下它:丢光了界面上就凭空少一帧,而这条只可能出现在
// MaxSessionBytes 被调到小于 MaxRetainedMessageBytes 的将来。
func TestTrimKeepsNewestEvenWhenOversized(t *testing.T) {
	t.Parallel()
	msgs := []StreamMessage{
		{ID: "old", Data: make([]byte, MaxSessionBytes)},
		{ID: "new", Data: make([]byte, MaxSessionBytes+1)},
	}
	got := TrimStreamMessages(msgs)
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("裁剪结果 = %+v,期望只留最新的一条", got)
	}
}

func TestTrimNoopWhenWithinLimits(t *testing.T) {
	t.Parallel()
	msgs := []WSMessage{{ID: "a", Data: []byte("1")}, {ID: "b", Data: []byte("2")}}
	got := TrimWSMessages(msgs)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("未超限时不该改动,实际 %+v", got)
	}
}
