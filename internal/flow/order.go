// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"context"
	"fmt"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
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

// ValidateHeaderPairs 拒绝名或值里含 CR / LF / NUL 的头。
//
// 保真写线是逐字节拼 "name: value\r\n"(见 forward.writeFaithfulRequest 与
// WriteResponseTo),绕开了 net/http 自带的头部校验:一个含 \r\n 的值就能拼出额外的头,
// 乃至一个完整的第二个请求 —— 后者会让上游多回一个响应,污染共享连接池并把响应错位串到
// 别的 flow。NUL 则可能在下游的 C 实现里截断。
//
// 真实两端的头经 textproto 逐行读入,不可能带这些字节,故本校验实际只拦构造器输入与
// 插件/规则的改写;其余控制字节照常放行 —— 保真的前提是不替两端做净化。
func ValidateHeaderPairs(pairs [][2]string) error {
	for _, kv := range pairs {
		if strings.ContainsAny(kv[0], "\r\n\x00") || strings.ContainsAny(kv[1], "\r\n\x00") {
			return fmt.Errorf("头部 %q 含 CR/LF/NUL,会破坏报文边界", kv[0])
		}
	}
	return nil
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

// OrderedRequestHeaders 以线缆顺序与大小写导出请求「当前」的头部。
//
// RawHeaders 是读取侧抓到的原始序列,此后规则 / 插件改的是 Header 与 Host,不会回写它;
// 直接交出 RawHeaders 等于交出改写前的值,而 URL / Body 已是改写后的 —— 拿去预填构造器
// 就会混出一份线上从未存在过的请求(例如规则换掉了 Authorization,编辑重发却用回旧凭据)。
// 故按 ApplyRequestToHTTP 出线时同样的规则 reconcile:原始序列只作顺序与大小写的骨架,
// 值一律取当前的。
//
// RawHeaders 为空(h2 入站、头部过大)时顺序信息本就不存在,退回规范化 map 并按名字排序
// —— 排序至少保证同一条 flow 每次导出的结果一致。
func OrderedRequestHeaders(r *Request) [][2]string {
	if len(r.RawHeaders) > 0 {
		vals := ToHTTPHeader(r.Header)
		// http.ReadRequest 把 Host 从 Header 挪到了 req.Host,不补回去 reconcile 会把
		// 原始序列里的 Host 行当成「已被删除」丢掉。Host 缺失时(合成的 flow)沿用原始值。
		if r.Host != "" {
			vals["Host"] = []string{r.Host}
		} else if h := rawHeaderValue(r.RawHeaders, "Host"); h != "" {
			vals["Host"] = []string{h}
		}
		return reconcileOrderedHeaders(r.RawHeaders, vals)
	}
	names := make([]string, 0, len(r.Header))
	for k := range r.Header {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([][2]string, 0, len(names))
	for _, k := range names {
		for _, v := range r.Header[k] {
			out = append(out, [2]string{k, v})
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
