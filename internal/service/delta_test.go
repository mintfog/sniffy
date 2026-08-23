// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// 增量推送的全部意义:载荷不随会话长度增长。时间线填满之后仍只带一条消息。
func TestWSDeltaCarriesOnlyNewMessage(t *testing.T) {
	t.Parallel()
	ws := &flow.WSSession{ID: "ws-1", URL: "wss://x/ws", Status: "open", StartTime: time.Now()}
	for i := 0; i < flow.MaxWSMessages; i++ {
		ws.Messages = append(ws.Messages, flow.WSMessage{
			ID: "old", Type: flow.WSText, Data: []byte(strings.Repeat("x", 4096)),
		})
	}
	ws.MessageCount = flow.MaxWSMessages
	// 与记录器同序:先入库并裁剪,再推送。
	added := flow.WSMessage{ID: "new", Type: flow.WSText, Data: []byte("hi"), Size: 2}
	ws.Messages = flow.TrimWSMessages(append(ws.Messages, added))
	ws.MessageCount++

	d := WSDelta(ws, &added)
	if len(d.Session.Messages) != 0 {
		t.Fatalf("增量的会话元数据不该带消息,实际 %d 条", len(d.Session.Messages))
	}
	if d.Message == nil || d.Message.ID != "new" || d.Message.Data != "hi" {
		t.Fatalf("增量消息 = %+v", d.Message)
	}
	if d.Retained != flow.MaxWSMessages {
		t.Fatalf("Retained = %d,期望 %d", d.Retained, flow.MaxWSMessages)
	}
	if d.Message.SessionID != ws.ID {
		t.Fatalf("增量消息的 sessionId = %q,期望 %q", d.Message.SessionID, ws.ID)
	}

	// 钉死「载荷与历史长度无关」:全量版是它的几百倍,回归时这条会先响。
	delta, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	full, err := json.Marshal(WSSessionDTO(ws))
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) > len(full)/100 {
		t.Fatalf("增量 %d 字节 vs 全量 %d 字节,增量没有真的变小", len(delta), len(full))
	}
}

func TestStreamDeltaCarriesOnlyNewMessage(t *testing.T) {
	t.Parallel()
	ss := &flow.StreamSession{ID: "st-1", Kind: flow.StreamSSE, Status: "open", StartTime: time.Now()}
	for i := 0; i < 100; i++ {
		ss.Messages = append(ss.Messages, flow.StreamMessage{ID: "old", Kind: flow.StreamSSE, Data: []byte("old")})
	}
	ss.MessageCount = 100
	// 记录器是「先入库再推送」,Retained 报的是入库后的条数。
	added := flow.StreamMessage{ID: "new", Kind: flow.StreamSSE, EventType: "tick", Data: []byte("now"), Seq: 100}
	ss.Messages = append(ss.Messages, added)
	ss.MessageCount++

	d := StreamDelta(ss, &added)
	if len(d.Session.Messages) != 0 {
		t.Fatalf("增量的会话元数据不该带消息,实际 %d 条", len(d.Session.Messages))
	}
	if d.Message == nil || d.Message.EventType != "tick" || d.Message.Seq != 100 {
		t.Fatalf("增量消息 = %+v", d.Message)
	}
	if d.Retained != 101 {
		t.Fatalf("Retained = %d,期望 101", d.Retained)
	}
}

// 建会话 / 补进程 / 关闭这类更新没有新消息,message 必须缺省而不是空对象 ——
// 前端据此区分「追加一条」和「只刷元数据」。
func TestDeltaWithoutMessageOmitsField(t *testing.T) {
	t.Parallel()
	end := time.Now()
	ws := &flow.WSSession{ID: "ws-1", Status: "closed", StartTime: time.Now(), EndTime: &end, MessageCount: 3}

	d := WSDelta(ws, nil)
	if d.Message != nil {
		t.Fatalf("无新增消息时 Message 应为 nil,实际 %+v", d.Message)
	}
	if d.Session.Status != "closed" || d.Session.EndTime == "" {
		t.Fatalf("元数据未带上终态: %+v", d.Session)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"message"`) {
		t.Fatalf("JSON 不该出现 message 字段: %s", raw)
	}
	// messages 恒为 [] 而非 null:前端拿到的形状与全量版一致,少一处判空。
	if !strings.Contains(string(raw), `"messages":[]`) {
		t.Fatalf("会话元数据的 messages 应为空数组: %s", raw)
	}
}

// MessageCount 兼作序号:它按真实收到的条数递增、不受裁剪影响,
// 前端靠它发现总线丢事件后回头整条重拉。
func TestDeltaMessageCountIsMonotonicUnderTrimming(t *testing.T) {
	t.Parallel()
	ws := &flow.WSSession{ID: "ws-1", Status: "open", StartTime: time.Now()}
	var counts []int
	for i := 0; i < flow.MaxWSMessages+5; i++ {
		payload, size := flow.RetainPayload([]byte("frame"))
		ws.MessageCount++
		ws.TotalSize += size
		m := flow.WSMessage{ID: "m", Type: flow.WSText, Data: payload, Size: size}
		ws.Messages = flow.TrimWSMessages(append(ws.Messages, m))
		counts = append(counts, WSDelta(ws, &m).Session.MessageCount)
	}
	for i := 1; i < len(counts); i++ {
		if counts[i] != counts[i-1]+1 {
			t.Fatalf("第 %d 次推送 messageCount 从 %d 跳到 %d", i, counts[i-1], counts[i])
		}
	}
	if len(ws.Messages) != flow.MaxWSMessages {
		t.Fatalf("裁剪后保留 %d 条,期望 %d", len(ws.Messages), flow.MaxWSMessages)
	}
}

// 被保留策略裁掉载荷的消息,Size 仍要报真实长度并打上 truncated,
// 否则界面上一个 64 MiB 的帧会显示成 1 MiB。
func TestMessageDTOReportsTrueSizeWhenTruncated(t *testing.T) {
	t.Parallel()
	payload, size := flow.RetainPayload(make([]byte, flow.MaxRetainedMessageBytes+2048))

	got := wsMessageDTO("ws-1", flow.WSMessage{ID: "m", Type: flow.WSBinary, Data: payload, Size: size})
	if got.Size != size {
		t.Fatalf("size = %d,期望真实长度 %d", got.Size, size)
	}
	if !got.Truncated {
		t.Fatal("被裁剪的消息应标记 truncated")
	}

	small := wsMessageDTO("ws-1", flow.WSMessage{ID: "m", Type: flow.WSText, Data: []byte("hi"), Size: 2})
	if small.Truncated {
		t.Fatal("完整消息不该标记 truncated")
	}
}
