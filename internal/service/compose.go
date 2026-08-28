// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"unicode/utf8"

	"github.com/mintfog/sniffy/internal/flow"
)

// ComposeSeedDTO 是以已捕获请求为蓝本打开构造器时返回的请求快照。
// 头部按线缆顺序、大小写和重复项返回；URL、Body 与头部来自同一 flow 状态。
type ComposeSeedDTO struct {
	FlowID  string      `json:"flowId"`
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers [][2]string `json:"headers"`
	// HeadersB64 是与 Headers 下标对齐的值字节旁路，文本位置为空串，全部值为合法 UTF-8 时省略。
	// 粒度不同于 HTTPRequestDTO.HeadersB64 的按名稀疏 map。
	HeadersB64 []string `json:"headersB64,omitempty"`
	Body       string   `json:"body,omitempty"`
	// BodySize 始终是原始体的字节数；BodyBinary 与 BodyTooLarge 都使 Body 为空，
	// 分别表示二进制形态与超出大小限制，此时构造器只能展示体积。
	BodySize     int  `json:"bodySize"`
	BodyBinary   bool `json:"bodyBinary,omitempty"`
	BodyTooLarge bool `json:"bodyTooLarge,omitempty"`
}

// MaxComposeSeedBytes 是可载入构造器编辑器的蓝本体上限，与发送侧上限取同一数值，
// 使编辑器载入的正文一定可发送。
const MaxComposeSeedBytes = flow.MaxComposeBodyBytes

// ComposeSeed 返回一条已捕获请求的保真快照。会话不存在或没有请求时 ok=false。
func (s *Service) ComposeSeed(id string) (*ComposeSeedDTO, bool) {
	f, ok := s.sessions.get(id)
	if !ok || f.Request == nil {
		return nil, false
	}
	r := f.Request
	seed := &ComposeSeedDTO{
		FlowID:   f.ID,
		Method:   r.Method,
		URL:      r.URL,
		Headers:  flow.OrderedRequestHeaders(r),
		BodySize: len(r.Body),
	}
	seed.HeadersB64 = flow.HeaderPairValuesB64(seed.Headers)
	// IsBinary 只嗅前若干字节，能通过文本判定的超大 JSON 才是内存风险，故长度判定排在形态判定之前。
	switch {
	case len(r.Body) == 0:
	case len(r.Body) > MaxComposeSeedBytes:
		seed.BodyTooLarge = true
	case utf8.Valid(r.Body) && !flow.IsBinary(r.Body):
		seed.Body = string(r.Body)
	default:
		seed.BodyBinary = true
	}
	return seed, true
}

// ComposeHeaderBasis 返回 ComposeSeed 对应的有序头部，供发送侧恢复 JSON 往返中的原始字节。
// 与 ComposeSeed 同源，因此界面提交的行与字节旁路可按下标对应。
func (s *Service) ComposeHeaderBasis(id string) ([][2]string, bool) {
	f, ok := s.sessions.get(id)
	if !ok || f.Request == nil {
		return nil, false
	}
	return flow.OrderedRequestHeaders(f.Request), true
}
