// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import "time"

// 流类型决定消息的分帧与展示方式。
const (
	StreamSSE   = "sse"   // text/event-stream:服务端推送事件
	StreamGRPC  = "grpc"  // application/grpc:h2 上的 length-prefixed 消息(可双向)
	StreamChunk = "chunk" // 通用分块流(NDJSON / application/stream+json / 不定长 chunked)
)

// MaxStreamMessages 是一条流会话在内存里保留的最近消息条数上限,语义与 MaxWSMessages 相同。
const MaxStreamMessages = 500

// StreamMessage 表示流中的一条记录，包括数据消息和 SSE 注释、控制块。
// Direction 复用 WSClientToServer / WSServerToClient，分别表示请求与响应方向。
type StreamMessage struct {
	ID        string `json:"id"`
	FlowID    string `json:"flowId"` // 所属 Flow.ID，同时也是 StreamSession.ID
	ConnID    string `json:"connId,omitempty"`
	URL       string `json:"url,omitempty"`
	Direction string `json:"direction"`
	Kind      string `json:"kind"`                // StreamSSE / StreamGRPC / StreamChunk
	EventType string `json:"eventType,omitempty"` // SSE 的 event 字段值，其余类型为空
	SSEType   string `json:"sseType,omitempty"`   // 仅 SSE 使用：空值为数据事件，其余为 SSEComment / SSEControl
	// Data 保存 SSE 拼接后的 data 载荷或注释、控制块原文；
	// gRPC 保存去掉 5 字节帧头的载荷，chunk 保存本次读入的字节。
	Data      []byte    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
	Seq       int       `json:"seq"` // 在本会话内的序号(从 0 递增)
	// Size 语义同 WSMessage.Size:载荷裁剪前的真实字节数,零值时以 len(Data) 为准。
	Size int64 `json:"size,omitempty"`
}

// PayloadSize 返回载荷裁剪前的真实字节数。
func (m StreamMessage) PayloadSize() int64 {
	if m.Size > 0 {
		return m.Size
	}
	return int64(len(m.Data))
}

// Truncated 报告 Data 是否只保留了载荷的前一段。
func (m StreamMessage) Truncated() bool { return m.Size > int64(len(m.Data)) }

// StreamSession 以 Flow.ID 为键保存流消息时间线；请求和响应头仍保存在 Flow 中。
// Messages 仅保留最近的记录，MessageCount 和 TotalSize 按裁剪前的消息累计。
type StreamSession struct {
	ID           string          `json:"id"` // == 所属 Flow.ID
	URL          string          `json:"url"`
	Kind         string          `json:"kind"`             // sse|grpc|chunk
	Method       string          `json:"method,omitempty"` // 请求方法(GET/POST...)
	StatusCode   int             `json:"statusCode,omitempty"`
	Status       string          `json:"status"` // open|closed
	StartTime    time.Time       `json:"startTime"`
	EndTime      *time.Time      `json:"endTime,omitempty"`
	MessageCount int             `json:"messageCount"`
	TotalSize    int64           `json:"totalSize"`
	Messages     []StreamMessage `json:"messages"`
	Process      *ProcessInfo    `json:"process,omitempty"`
}
