// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import "time"

// WS 方向。
const (
	WSClientToServer = "client->server"
	WSServerToClient = "server->client"
)

// WS 帧类型。
const (
	WSText   = "text"
	WSBinary = "binary"
	WSClose  = "close"
	WSPing   = "ping"
	WSPong   = "pong"
)

// MaxWSMessages 是一条 WS 会话在内存里保留的最近消息条数上限。
// 计数与总大小仍然累计 —— 上限只裁剪展示用的时间线,不裁剪统计。
// 条数只是三道闸门之一,另两道见 retention.go。
const MaxWSMessages = 500

// WSMessage 表示一条 WebSocket 消息(单向一帧)。
type WSMessage struct {
	ID        string    `json:"id"`
	FlowID    string    `json:"flowId"`           // 所属 WebSocket 会话(升级请求的 Flow)
	ConnID    string    `json:"connId,omitempty"` //
	URL       string    `json:"url,omitempty"`    //
	Direction string    `json:"direction"`        // client->server | server->client
	Type      string    `json:"type"`             // text|binary|close|ping|pong
	Data      []byte    `json:"data"`             //
	Timestamp time.Time `json:"timestamp"`        //
	// Size 是载荷裁剪前的真实字节数(见 MaxRetainedMessageBytes)。
	// 零值表示「未经保留策略处理」,此时以 len(Data) 为准 —— 插件钩子拿到的
	// WSMessage 由管道就地构造,不走 RetainPayload。
	Size int64 `json:"size,omitempty"`
}

// PayloadSize 返回载荷裁剪前的真实字节数。
func (m WSMessage) PayloadSize() int64 {
	if m.Size > 0 {
		return m.Size
	}
	return int64(len(m.Data))
}

// Truncated 报告 Data 是否只保留了载荷的前一段。
func (m WSMessage) Truncated() bool { return m.Size > int64(len(m.Data)) }

// WSSession 表示一条 WebSocket 会话(用于 UI 展示与存储)。
type WSSession struct {
	ID           string       `json:"id"`
	URL          string       `json:"url"`
	Status       string       `json:"status"` // open|closed
	StartTime    time.Time    `json:"startTime"`
	EndTime      *time.Time   `json:"endTime,omitempty"`
	MessageCount int          `json:"messageCount"`
	TotalSize    int64        `json:"totalSize"`
	Messages     []WSMessage  `json:"messages"`
	Process      *ProcessInfo `json:"process,omitempty"`
}
