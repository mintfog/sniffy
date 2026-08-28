// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package flow 定义贯穿系统的统一流量契约 Flow。
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
	Response *Response      `json:"response,omitempty"` // 上游响应或 mock 响应
	Timing   Timing         `json:"timing"`             //
	State    FlowState      `json:"state"`              //
	PausedAt Phase          `json:"pausedAt,omitempty"` // 断点载荷标记暂停发生在请求或响应阶段
	Modified bool           `json:"modified"`           // 是否被任意插件/断点改动过
	Tags     []string       `json:"tags,omitempty"`     //
	Error    string         `json:"error,omitempty"`    //
	Metadata map[string]any `json:"metadata,omitempty"` // 跨钩子存活,记录原始 Content-Encoding 等

	// process 由 procinfo 在独立 goroutine 中异步补齐，使用原子指针支持并发读写。
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

	// RawHeaders 是请求头的线缆序列(顺序、原始大小写、重复头);h2 入站或头部过大时为空。
	// 规则与插件改的是 Header,不回写这里 —— 取当前头部请用 OrderedRequestHeaders。
	RawHeaders [][2]string `json:"rawHeaders,omitempty"`

	// 以下私有字段记录入站请求体的原始线缆形态，供 body 未改动时保真回放。
	// 线缆、插件、UI 与存储统一使用 Body 的 identity 视图。
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

// OriginalEncodedBody 在 body 未改动且存在原始编码时返回编码字节；其余情况返回 nil。
func (r *Request) OriginalEncodedBody(currentBody []byte) []byte {
	if r.origEncoding == "" || r.origEncodedBody == nil {
		return nil
	}
	if !bytes.Equal(currentBody, r.origDecodedBody) {
		return nil // body 已更新，按 identity 字节处理。
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
	// 在读响应头时抓取并经 ctx 回填;h2 / 回退 / mock 时为空。
	// 规则与插件改的是 Header,不回写这里 —— 取当前头部请用 OrderedResponseHeaders。
	RawHeaders [][2]string `json:"rawHeaders,omitempty"`

	// 以下私有字段记录上游响应的原始线缆形态（状态行与编码体），供 body 未改动时回放。
	origStatusLine  string // 原始状态行,如 "HTTP/1.1 200 OK"
	origEncodedBody []byte // 原始(编码后)线缆字节
	origDecodedBody []byte // 解码后的字节(== 构造时的 Body)
	origEncoding    string // 上游响应 Content-Encoding

	// 透传旁路响应边转发边写入缓存文件，Body 保持为空，完整字节按 bodyFile 读取。
	bodyFile string
	bodySize int64

	// truncated 表示 Body 只读到一半（上游中途断流），写回客户端时沿用原始长度。
	truncated bool
}

// MarkTruncated 标记响应体未完整读取；写回客户端时沿用上游宣告的 Content-Length，客户端据短读识别截断。
func (r *Response) MarkTruncated() { r.truncated = true }

// ClearTruncated 清除截断标记，供断点改包或插件 mock 写入完整正文后使用。
func (r *Response) ClearTruncated() { r.truncated = false }

// SetOriginalHead 记录上游响应的原始状态行(供写回客户端时保真回放)。
func (r *Response) SetOriginalHead(statusLine string) { r.origStatusLine = statusLine }

// SetOriginalBody 记录响应体原始线缆字节与编码(供 body 未改动时原样回放)。
func (r *Response) SetOriginalBody(encoded, decoded []byte, encoding string) {
	r.origEncodedBody = encoded
	r.origDecodedBody = decoded
	r.origEncoding = encoding
}

// OriginalEncodedBody 在 body 未被改动且存在原始编码时返回编码字节；其余情况返回 nil。
func (r *Response) OriginalEncodedBody(currentBody []byte) []byte {
	if r.origEncoding == "" || r.origEncodedBody == nil {
		return nil
	}
	if !bytes.Equal(currentBody, r.origDecodedBody) {
		return nil
	}
	return r.origEncodedBody
}

// SetPassthroughBody 记录透传响应体的缓存路径与实际转发字节数；Body 保持为空。
func (r *Response) SetPassthroughBody(path string, size int64) {
	r.bodyFile = path
	r.bodySize = size
}

// BodyFile 返回响应体落盘副本的路径与字节数;未落盘时 path 为空串。
func (r *Response) BodyFile() (string, int64) { return r.bodyFile, r.bodySize }

// BodyLen 返回响应体字节数；透传旁路读取旁路记录值，其余响应读取 len(Body)。
func (r *Response) BodyLen() int64 {
	if r.bodySize > 0 {
		return r.bodySize
	}
	return int64(len(r.Body))
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
		// 随机源不可用时使用时间派生 ID。
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

// Clone 返回 Flow 的深拷贝快照，供事件与存储发布独立于后续处理的副本。
// process 通过原子读取后挂到副本上。
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
		PausedAt: f.PausedAt,
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
