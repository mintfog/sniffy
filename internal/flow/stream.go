// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import "time"

// 双向流(SSE / gRPC / 分块流)的数据契约。
//
// 与 WebSocket 类似,流式响应/请求无法装进单个 Flow.Body —— 它是一串随时间到达的
// 消息。故仿照 WSSession/WSMessage,用 StreamSession 承载「一条流的消息时间线」,
// 以所属 Flow 的 ID 为键(StreamSession.ID == Flow.ID),供 UI 在该 Flow 的详情里
// 实时展示逐条消息。普通的请求/响应头仍按常规 Flow 记录。

// StreamKind 标识流的类型(决定消息如何分帧/展示)。
const (
	StreamSSE   = "sse"   // text/event-stream:服务端推送事件
	StreamGRPC  = "grpc"  // application/grpc:h2 上的 length-prefixed 消息(可双向)
	StreamChunk = "chunk" // 通用分块流(NDJSON / application/stream+json / 不定长 chunked)
)

// StreamMessage 表示一条流消息(单向一帧)。Direction 复用 WS 的取值,便于前端统一展示:
//   - server->client:响应方向(SSE 事件、gRPC 服务端消息)。
//   - client->server:请求方向(gRPC 客户端消息,双向流)。
type StreamMessage struct {
	ID        string    `json:"id"`
	FlowID    string    `json:"flowId"`              // 所属 Flow(== StreamSession.ID)
	ConnID    string    `json:"connId,omitempty"`    //
	URL       string    `json:"url,omitempty"`       //
	Direction string    `json:"direction"`           // client->server | server->client
	Kind      string    `json:"kind"`                // sse|grpc|chunk
	EventType string    `json:"eventType,omitempty"` // SSE 的 event: 名;其余为空
	Data      []byte    `json:"data"`                // SSE:事件原文;gRPC:消息载荷(去 5 字节前缀);chunk:原始分块
	Timestamp time.Time `json:"timestamp"`           //
	Seq       int       `json:"seq"`                 // 在本会话内的序号(从 0 递增)
}

// StreamSession 表示一条流式传输的消息时间线(用于 UI 展示与存储)。
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
