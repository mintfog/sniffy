// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package flow 定义贯穿整个系统的统一流量契约 Flow。
//
// Flow 是 engine、插件脚本(goja)、UI(断点编辑)、会话存储与线缆传输
// 之间共用的唯一数据形状,取代历史上互相漂移的三套结构
// (plugins.InterceptContext / web_api 的 HTTPSession / 前端 TS 类型)。
package flow

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// FlowState 描述一个 Flow 当前所处的生命周期阶段。
type FlowState string

const (
	StatePending            FlowState = "pending"              // 已读到请求,尚未转发
	StateAwaitingResponse   FlowState = "awaiting_response"    // 已转发上游,等待响应
	StateCompleted          FlowState = "completed"            // 正常完成
	StateBlocked            FlowState = "blocked"              // 被插件 abort 阻断
	StateMocked             FlowState = "mocked"               // 由插件 mock 直接响应(未打上游)
	StateErrored            FlowState = "errored"              // 处理过程中出错
	StatePausedAtBreakpoint FlowState = "paused_at_breakpoint" // 命中断点,等待 UI 手动放行
)

// Phase 表示拦截发生的阶段(请求 / 响应)。
type Phase string

const (
	PhaseRequest  Phase = "request"
	PhaseResponse Phase = "response"
)

// Protocol 取值。
const (
	ProtoHTTP  = "http"
	ProtoHTTPS = "https"
	ProtoWS    = "ws"
	ProtoWSS   = "wss"
)

// Flow 是一次请求/响应往返的完整描述,全程以 ID 串联。
type Flow struct {
	ID       string         `json:"id"`                 // 请求读入时生成,替代脆弱的 URL 配对
	ConnID   string         `json:"connId,omitempty"`   // 所属连接,用于分组与 WebSocket
	Protocol string         `json:"protocol"`           // http|https|ws|wss
	Request  *Request       `json:"request"`            //
	Response *Response      `json:"response,omitempty"` // 上游响应或 mock 之前为 nil
	Process  *ProcessInfo   `json:"process,omitempty"`  // 进程信息(异步补齐,可能为 nil)
	Timing   Timing         `json:"timing"`             //
	State    FlowState      `json:"state"`              //
	Modified bool           `json:"modified"`           // 是否被任意插件/断点改动过
	Tags     []string       `json:"tags,omitempty"`     //
	Error    string         `json:"error,omitempty"`    //
	Metadata map[string]any `json:"metadata,omitempty"` // 跨钩子存活,记录原始 Content-Encoding 等
}

// Request 表示一次出站请求。Body 永远是 identity 解码后的原始字节,
// 原始传输编码记录在 Flow.Metadata 中,出站时由 codec 决定如何重建。
type Request struct {
	Method   string              `json:"method"`
	URL      string              `json:"url"` // 完整 URL(scheme+host+path+query)
	Host     string              `json:"host"`
	Path     string              `json:"path"`
	Proto    string              `json:"proto"`
	Header   map[string][]string `json:"header"`
	Body     []byte              `json:"body,omitempty"`
	ClientIP string              `json:"clientIp,omitempty"`
}

// Response 表示一次响应。Body 语义同 Request.Body。
type Response struct {
	Status     int                 `json:"status"`
	StatusText string              `json:"statusText,omitempty"`
	Header     map[string][]string `json:"header"`
	Body       []byte              `json:"body,omitempty"`
}

// ProcessInfo 镜像 pkg/process.ProcessInfo,并携带前端所需的图标字段。
type ProcessInfo struct {
	PID          uint32 `json:"pid,omitempty"`
	Name         string `json:"name,omitempty"`
	Path         string `json:"path,omitempty"`
	User         string `json:"user,omitempty"`
	HasIcon      bool   `json:"hasIcon,omitempty"`
	IconData     string `json:"iconData,omitempty"` // base64
	IconType     string `json:"iconType,omitempty"` // png|svg
	IconSize     int    `json:"iconSize,omitempty"`
	IconCategory string `json:"iconCategory,omitempty"`
}

// Timing 记录关键时间点与衍生耗时。
type Timing struct {
	RequestAt   time.Time `json:"requestAt"`
	ResponseAt  time.Time `json:"responseAt,omitempty"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
	DurationMs  int64     `json:"durationMs,omitempty"`
	TTFBMs      int64     `json:"ttfbMs,omitempty"`
}

// NewID 生成一个 16 字节的随机十六进制 ID。
// 不引入外部 UUID 依赖;碰撞概率在本场景可忽略。
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read 实际上不会失败;退化为基于时间的弱 ID 以保证可用。
		return "flow-" + hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

// New 创建一个处于 Pending 状态的新 Flow。
func New(protocol string) *Flow {
	return &Flow{
		ID:       NewID(),
		Protocol: protocol,
		State:    StatePending,
		Timing:   Timing{RequestAt: time.Now()},
		Metadata: make(map[string]any),
	}
}
