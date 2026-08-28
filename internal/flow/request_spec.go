// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

// RequestSpec 描述一次由 UI 直接发起的请求（空白构造或编辑后重发）。
// 它与 Flow、Decision 一样是跨 transport 的统一契约。
type RequestSpec struct {
	// Kind 标识收发模式，空串与 SpecKindHTTP 等价，按一次性 HTTP 往返处理。
	// SpecKindGraphQL 在后端与 HTTP 完全同路:body 已由前端合成好 JSON,这里只多打一个标签。
	Kind   string `json:"kind,omitempty"`
	Method string `json:"method"`
	URL    string `json:"url"`
	// Headers 是有序的头部列表,保留用户输入的大小写与重复项,并按该顺序原样写线
	// （见 ApplyRequestToHTTP 的 RawHeaders 分支）。
	Headers [][2]string `json:"headers"`
	// HeadersB64 是与 Headers 下标对齐的值字节旁路；非空项按其解码后的字节写线，
	// 空项采用对应 Headers 值。列表长度必须与 Headers 一致。
	HeadersB64 []string `json:"headersB64,omitempty"`
	Body       string   `json:"body"`
	// FromID 是蓝本 flow,用于溯源标记(resent 标签与 resentFrom 元数据)。
	// 未提供旁路时，发送侧使用该 flow 的有序头部作为字节还原基准。
	FromID string `json:"fromId,omitempty"`
	// ViaPipeline 决定这次请求是否经过插件、重写规则与断点。
	// SpecKindWS 下只作用于逐帧消息，握手不过管道；SpecKindSSE 的首个往返照常经过。
	ViaPipeline bool `json:"viaPipeline"`
}

// SpecKind 取值:构造器要按哪种模式收发。
const (
	SpecKindHTTP    = "http"
	SpecKindGraphQL = "graphql"
	SpecKindSSE     = "sse"
	SpecKindWS      = "ws"
)

// MaxComposeBodyBytes 是构造器请求体的字节上限。
// Body 整体读入 Flow 并存入会话，限制在 app/service 边界统一执行。
const MaxComposeBodyBytes = 8 << 20
