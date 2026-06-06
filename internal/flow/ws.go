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
}

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
