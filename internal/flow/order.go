// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"context"
	"net/http"
	"net/textproto"
	"sort"
)

// 本文件承载「无侵入转发」所需的头部顺序/大小写保真:
//   - 读取侧把原始请求头(保留顺序与原始大小写)经 ctx 传进 BuildRequestFlow,
//     存入 Flow.Request.RawHeaders。
//   - ApplyRequestToHTTP 把(可能被插件改过的)头值表与原始顺序合并成最终线缆序列,
//     再经 ctx 交给保真转发器(internal/forward)按原样写线。
//
// net/http 的 Header 是 map、写出时按字母排序并规范化名字大小写,无任何 hook 可改,
// 故保真只能绕开它:把「顺序+大小写」作为旁路信息单独保存与回放。

type rawHeadersKeyT struct{}
type orderedHeadersKeyT struct{}
type respCaptureKeyT struct{}

var (
	rawHeadersKey     rawHeadersKeyT
	orderedHeadersKey orderedHeadersKeyT
	respCaptureKey    respCaptureKeyT
)

// ResponseCapture 收集上游响应的原始状态行与头序列(顺序+大小写),由保真转发器
// (internal/forward)在读到响应头时填充,供响应写回客户端时按原样回放。经请求 ctx 传递。
type ResponseCapture struct {
	StatusLine string      // 如 "HTTP/1.1 200 OK"
	Headers    [][2]string // 原始头序列
}

// WithResponseCapture 在请求 ctx 中装入响应头收集器,供转发器读到响应头时回填。
func WithResponseCapture(ctx context.Context, c *ResponseCapture) context.Context {
	return context.WithValue(ctx, respCaptureKey, c)
}

// ResponseCaptureFrom 取出响应头收集器(未装入时返回 false)。
func ResponseCaptureFrom(ctx context.Context) (*ResponseCapture, bool) {
	c, ok := ctx.Value(respCaptureKey).(*ResponseCapture)
	return c, ok && c != nil
}

// WithRawHeaders 把读取侧抓到的原始请求头(顺序+大小写)放进 ctx。
func WithRawHeaders(ctx context.Context, raw [][2]string) context.Context {
	return context.WithValue(ctx, rawHeadersKey, raw)
}

// RawHeadersFrom 取出原始请求头序列。
func RawHeadersFrom(ctx context.Context) ([][2]string, bool) {
	v, ok := ctx.Value(rawHeadersKey).([][2]string)
	return v, ok && len(v) > 0
}

// WithOrderedHeaders 把最终线缆头序列放进 ctx,供保真转发器写线。
func WithOrderedHeaders(ctx context.Context, ordered [][2]string) context.Context {
	return context.WithValue(ctx, orderedHeadersKey, ordered)
}

// OrderedHeadersFrom 取出最终线缆头序列;缺省(如 h2 入站、头部过大无法保真)返回 false,
// 转发器据此回退到标准 net/http。
func OrderedHeadersFrom(ctx context.Context) ([][2]string, bool) {
	v, ok := ctx.Value(orderedHeadersKey).([][2]string)
	if !ok || len(v) == 0 {
		return nil, false
	}
	return v, true
}

// reconcileOrderedHeaders 把原始头序列(顺序+大小写)与当前头值表(可能被插件改过)
// 合并成最终线缆序列:
//   - 沿原始顺序逐项,用当前值回填、原样保留名字大小写;
//   - 原始有、当前已无的头(被删 / 多余值)跳过;
//   - 当前有、原始没有的头(插件新增、合成的 Content-Length 等)按规范名追加在尾部
//     (排序以保证输出稳定)。
//
// 未改动的请求 → 输出与线上逐字一致;改动过的请求 → 未改头仍保序保真,只有被改/新增的部分变化。
func reconcileOrderedHeaders(raw [][2]string, vals http.Header) [][2]string {
	remaining := make(map[string][]string, len(vals))
	for k, vv := range vals {
		cp := make([]string, len(vv))
		copy(cp, vv)
		remaining[k] = cp
	}

	out := make([][2]string, 0, len(raw)+2)
	for _, kv := range raw {
		ck := textproto.CanonicalMIMEHeaderKey(kv[0])
		q := remaining[ck]
		if len(q) == 0 {
			continue // 被删除,或其值已全部输出
		}
		out = append(out, [2]string{kv[0], q[0]})
		remaining[ck] = q[1:]
	}

	leftover := make([]string, 0, len(remaining))
	for ck, q := range remaining {
		if len(q) > 0 {
			leftover = append(leftover, ck)
		}
	}
	sort.Strings(leftover)
	for _, ck := range leftover {
		for _, v := range remaining[ck] {
			out = append(out, [2]string{ck, v})
		}
	}
	return out
}

// cloneHTTPHeader 返回 http.Header 的深拷贝(键已是规范名)。
func cloneHTTPHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}
