// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"unicode/utf8"

	"github.com/mintfog/sniffy/internal/flow"
)

// ComposeSeedDTO 是「以某条已捕获请求为蓝本打开构造器」时回传的请求快照。
//
// 它与 HTTPSessionDTO.Request 的差别只有一处但很关键:头部按线缆顺序与大小写返回、
// 保留重复项,而不是 flattenHeaders 折叠出的 map。构造器要让用户逐项编辑再原样重放,
// 折叠会悄悄丢掉重复的 Set-Cookie / Accept 这类头,也丢掉服务端可能据以指纹识别的顺序。
//
// 头部的值取自流经管道之后的当前状态(见 flow.OrderedRequestHeaders):URL 与 Body 也是
// 改写后的,三者必须同代,否则预填出来的是一份线上从未发生过的请求。
type ComposeSeedDTO struct {
	FlowID  string      `json:"flowId"`
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers [][2]string `json:"headers"`
	Body    string      `json:"body,omitempty"`
	// BodySize 是原始体的字节数;BodyBinary 为真时 Body 为空——构造器是文本编辑器,
	// 二进制体无法在其中往返,只能如实告诉用户「这条的体不会被带上」。
	BodySize   int  `json:"bodySize"`
	BodyBinary bool `json:"bodyBinary,omitempty"`
	// BodyTooLarge 与 BodyBinary 同构:体过大时 Body 同样为空,理由不同。
	BodyTooLarge bool `json:"bodyTooLarge,omitempty"`
}

// MaxComposeSeedBytes 是能被载进构造器编辑器的蓝本体上限。
//
// 请求体走的是 flow.BuildRequestFlow 里无上限的 io.ReadAll,一次几百 MB 的上传本来就
// 整条躺在会话存储里;把它再转成 string、过一遍 JSON/Bridge、送进 WebView,是同一份数据
// 的第四、五份副本 —— 桌面进程多半就卡死在这一步。超限时按 BodyBinary 的老办法处理:
// 只报大小、明说载不进编辑器,而不是悄悄截断成一份发出去就变味的请求。
//
// 与 flow.MaxComposeBodyBytes 取同一个数:载得进来的就一定发得出去。
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
	switch {
	case len(r.Body) == 0:
	case len(r.Body) > MaxComposeSeedBytes:
		// 先判大小再判文本:IsBinary 只嗅前若干字节,但真正拖垮进程的是整体长度,
		// 一个 300 MB 的 JSON 完全通得过文本判定。
		seed.BodyTooLarge = true
	case utf8.Valid(r.Body) && !flow.IsBinary(r.Body):
		seed.Body = string(r.Body)
	default:
		seed.BodyBinary = true
	}
	return seed, true
}
