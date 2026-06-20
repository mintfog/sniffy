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
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
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
	Timing   Timing         `json:"timing"`             //
	State    FlowState      `json:"state"`              //
	Modified bool           `json:"modified"`           // 是否被任意插件/断点改动过
	Tags     []string       `json:"tags,omitempty"`     //
	Error    string         `json:"error,omitempty"`    //
	Metadata map[string]any `json:"metadata,omitempty"` // 跨钩子存活,记录原始 Content-Encoding 等

	// process 为发起进程信息,由 procinfo 在独立 goroutine 中异步补齐,
	// 与处理/序列化侧并发,故以原子指针读写(可能为 nil)。
	process atomic.Pointer[ProcessInfo]
}

// Process 返回异步补齐的发起进程信息,未解析到时为 nil。
func (f *Flow) Process() *ProcessInfo { return f.process.Load() }

// SetProcess 挂上发起进程信息(并发安全)。
func (f *Flow) SetProcess(p *ProcessInfo) { f.process.Store(p) }

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

	// RawHeaders 是客户端线上原始请求头序列(保留顺序与原始大小写,含重复头)。
	// 仅在读取侧(HTTP/1.x)抓得到;h2 入站或头部过大时为空。Header 是供插件/UI/规则
	// 编辑的规范化视图,RawHeaders 仅用于出站时按原样回放顺序/大小写,二者不互相覆盖。
	RawHeaders [][2]string `json:"rawHeaders,omitempty"`

	// 以下私有字段记录入站请求体的原始线缆形态,供出站时在「插件未改动 body」的前提下
	// 按原样回放(保真),避免把客户端的 gzip/br/zstd 等压缩体重编码成 identity。
	// 故意不导出 / 不序列化:线缆、插件(goja)、UI、存储看到的只是 Body 的 identity 视图。
	origEncodedBody []byte // 原始(编码后)线缆字节
	origDecodedBody []byte // 解码后的字节(== 构造时的 Body),用于判定 body 是否被改动
	origEncoding    string // 客户端原始 Content-Encoding(非空才考虑保真回放)
}

// SetOriginalBody 记录请求体的原始线缆字节与编码(供 flow 包内的转换函数判定与回放)。
// encoded 为线上原始字节,decoded 为其 identity 解码结果,encoding 为 Content-Encoding。
func (r *Request) SetOriginalBody(encoded, decoded []byte, encoding string) {
	r.origEncodedBody = encoded
	r.origDecodedBody = decoded
	r.origEncoding = encoding
}

// OriginalEncodedBody 在「body 未被改动」且确有原始编码时,返回应原样回放的编码后字节;
// 否则返回 nil(出站走 identity 重建)。currentBody 为(可能被插件改过的)当前 body。
func (r *Request) OriginalEncodedBody(currentBody []byte) []byte {
	if r.origEncoding == "" || r.origEncodedBody == nil {
		return nil
	}
	if !bytes.Equal(currentBody, r.origDecodedBody) {
		return nil // body 被改动:无法保真,走 identity
	}
	return r.origEncodedBody
}

// Response 表示一次响应。Body 语义同 Request.Body。
type Response struct {
	Status     int                 `json:"status"`
	StatusText string              `json:"statusText,omitempty"`
	Header     map[string][]string `json:"header"`
	Body       []byte              `json:"body,omitempty"`
	// Trailer 为 HTTP/2 响应尾部(如 gRPC 的 grpc-status / grpc-message),
	// 在 body 读尽后才可得;HTTP/1.x 通常为空。
	Trailer map[string][]string `json:"trailer,omitempty"`

	// RawHeaders 是上游响应线上原始头序列(顺序+大小写),由保真转发器(internal/forward)
	// 在读响应头时抓取并经 ctx 回填;h2 / 回退 / mock 时为空。用于写回客户端时按原样回放。
	RawHeaders [][2]string `json:"rawHeaders,omitempty"`

	// 以下私有字段记录上游响应的原始线缆形态(状态行 + 编码体),供写回客户端时在
	// 「插件未改动 body」的前提下原样回放。不导出 / 不序列化。
	origStatusLine  string // 原始状态行,如 "HTTP/1.1 200 OK"
	origEncodedBody []byte // 原始(编码后)线缆字节
	origDecodedBody []byte // 解码后的字节(== 构造时的 Body)
	origEncoding    string // 上游响应 Content-Encoding
}

// SetOriginalHead 记录上游响应的原始状态行(供写回客户端时保真回放)。
func (r *Response) SetOriginalHead(statusLine string) { r.origStatusLine = statusLine }

// SetOriginalBody 记录响应体原始线缆字节与编码(供 body 未改动时原样回放)。
func (r *Response) SetOriginalBody(encoded, decoded []byte, encoding string) {
	r.origEncodedBody = encoded
	r.origDecodedBody = decoded
	r.origEncoding = encoding
}

// OriginalEncodedBody 在「body 未被改动」且确有原始编码时返回应原样回放的编码后字节;
// 否则返回 nil(走 identity)。
func (r *Response) OriginalEncodedBody(currentBody []byte) []byte {
	if r.origEncoding == "" || r.origEncodedBody == nil {
		return nil
	}
	if !bytes.Equal(currentBody, r.origDecodedBody) {
		return nil
	}
	return r.origEncodedBody
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

// Clone 返回 Flow 的深拷贝快照,用于在事件/存储中发布一个不随后续处理而变化的副本,
// 避免序列化方(异步)与处理方(就地改写 Request/Response/State)发生数据竞态。
// 不复制异步进程指针之外的并发约束;process 用原子读取后挂到副本上。
func (f *Flow) Clone() *Flow {
	if f == nil {
		return nil
	}
	cp := &Flow{
		ID:       f.ID,
		ConnID:   f.ConnID,
		Protocol: f.Protocol,
		Timing:   f.Timing,
		State:    f.State,
		Modified: f.Modified,
		Error:    f.Error,
	}
	if f.Request != nil {
		r := *f.Request
		r.Header = cloneStrMap(f.Request.Header)
		r.Body = cloneBytes(f.Request.Body)
		if f.Request.RawHeaders != nil {
			r.RawHeaders = append([][2]string(nil), f.Request.RawHeaders...)
		}
		cp.Request = &r
	}
	if f.Response != nil {
		r := *f.Response
		r.Header = cloneStrMap(f.Response.Header)
		r.Body = cloneBytes(f.Response.Body)
		r.Trailer = cloneStrMap(f.Response.Trailer)
		cp.Response = &r
	}
	if f.Tags != nil {
		cp.Tags = append([]string(nil), f.Tags...)
	}
	if f.Metadata != nil {
		m := make(map[string]any, len(f.Metadata))
		for k, v := range f.Metadata {
			m[k] = v
		}
		cp.Metadata = m
	}
	if p := f.Process(); p != nil {
		cp.SetProcess(p)
	}
	return cp
}

func cloneStrMap(h map[string][]string) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}
