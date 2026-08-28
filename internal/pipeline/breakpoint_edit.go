// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/mintfog/sniffy/internal/flow"
)

// ErrBreakpointNotFound 表示 flow 已完成放行、阻断或超时。
var ErrBreakpointNotFound = errors.New("断点不存在或已解除")

// BreakpointEdit 是 UI 放行时提交的增量编辑内容，缺省字段表示保持原值。
// 头部使用有序键值对，正文使用 identity 文本；请求与响应对象按字段增量更新。
type BreakpointEdit struct {
	Request  *RequestEdit  `json:"request,omitempty"`
	Response *ResponseEdit `json:"response,omitempty"`
}

// RequestEdit 是请求侧的编辑。Headers 为 nil 保持现有头部，非 nil（含空列表）整体重设。
type RequestEdit struct {
	Method  *string     `json:"method,omitempty"`
	URL     *string     `json:"url,omitempty"`
	Headers [][2]string `json:"headers,omitempty"`
	// HeadersB64 是与 Headers 下标对齐的值字节旁路（全量列表，文本位置为空串），
	// 非空项按其解码后的字节写线,空项采用对应 Headers 值；列表长度必须与 Headers 一致。
	HeadersB64 []string `json:"headersB64,omitempty"`
	// Body 使用 identity 文本；出站编码由 flow.ApplyRequestToHTTP 按最终 body 重算。
	Body *string `json:"body,omitempty"`
}

// ResponseEdit 是响应侧的编辑,字段语义同 RequestEdit。
type ResponseEdit struct {
	Status     *int        `json:"status,omitempty"`
	StatusText *string     `json:"statusText,omitempty"`
	Headers    [][2]string `json:"headers,omitempty"`
	HeadersB64 []string    `json:"headersB64,omitempty"`
	Body       *string     `json:"body,omitempty"`
}

// resolveHeaders 将两侧编辑中的头部字节旁路解析为有序头对，写回 Headers 并清空旁路字段。
// Headers 或 HeadersB64 任一提供值时都会触发解析，结果供后续校验与应用使用。
func (e *BreakpointEdit) resolveHeaders(reqBasis, resBasis [][2]string) error {
	if e == nil {
		return nil
	}
	if r := e.Request; r != nil && (r.Headers != nil || len(r.HeadersB64) > 0) {
		pairs, err := flow.ResolveEditedHeaders(r.Headers, r.HeadersB64, reqBasis)
		if err != nil {
			return fmt.Errorf("请求头: %w", err)
		}
		r.Headers = pairs
		r.HeadersB64 = nil
	}
	if r := e.Response; r != nil && (r.Headers != nil || len(r.HeadersB64) > 0) {
		pairs, err := flow.ResolveEditedHeaders(r.Headers, r.HeadersB64, resBasis)
		if err != nil {
			return fmt.Errorf("响应头: %w", err)
		}
		r.Headers = pairs
		r.HeadersB64 = nil
	}
	return nil
}

// Validate 检查编辑内容的报文格式、大小、方法、URL 和状态码。
func (e *BreakpointEdit) Validate() error {
	if e == nil {
		return nil
	}
	if r := e.Request; r != nil {
		if err := validateHeaderEdit(r.Headers); err != nil {
			return err
		}
		if err := validateBodyEdit(r.Body); err != nil {
			return err
		}
		if r.Method != nil && !validMethodToken(*r.Method) {
			return fmt.Errorf("请求方法 %q 含非法字符", *r.Method)
		}
		if r.URL != nil {
			u, err := url.Parse(*r.URL)
			if err != nil {
				return fmt.Errorf("URL 无法解析: %w", err)
			}
			if u.Host == "" {
				return fmt.Errorf("URL 缺少主机名: %s", *r.URL)
			}
		}
	}
	if r := e.Response; r != nil {
		if err := validateHeaderEdit(r.Headers); err != nil {
			return err
		}
		if err := validateBodyEdit(r.Body); err != nil {
			return err
		}
		if r.Status != nil && (*r.Status < 100 || *r.Status > 599) {
			return fmt.Errorf("状态码 %d 不在 100-599 之间", *r.Status)
		}
	}
	return nil
}

// validateAgainst 根据当前 flow 状态校验编辑内容。
func (e *BreakpointEdit) validateAgainst(f *flow.Flow) error {
	if e == nil || e.Response == nil || e.Response.Status == nil || f == nil {
		return nil
	}
	// 流式与透传响应的正文已按原始协议中继，状态码变更需保持有体/无体语义一致。
	if bodylessStatus(*e.Response.Status) && relaysBodyVerbatim(f) {
		return fmt.Errorf("流式 / 透传响应的正文由上游原样中继,不能改成无体状态码 %d", *e.Response.Status)
	}
	return nil
}

// bodylessStatus 报告该状态码按 RFC 9110 不携带 body。
func bodylessStatus(code int) bool {
	return code == http.StatusNoContent || code == http.StatusNotModified || (code >= 100 && code < 200)
}

// relaysBodyVerbatim 报告响应体是否由流式或透传路径直接中继。
func relaysBodyVerbatim(f *flow.Flow) bool {
	if f.Metadata == nil {
		return false
	}
	_, streamed := f.Metadata["stream"]
	_, passthrough := f.Metadata["passthrough"]
	return streamed || passthrough
}

// validateHeaderEdit 检查原样写线头部的报文字符。
func validateHeaderEdit(pairs [][2]string) error {
	if pairs == nil {
		return nil
	}
	return flow.ValidateHeaderPairs(pairs)
}

// validateBodyEdit 使用与构造器相同的正文大小上限。
func validateBodyEdit(body *string) error {
	if body == nil {
		return nil
	}
	if len(*body) > flow.MaxComposeBodyBytes {
		return fmt.Errorf("消息体 %d 字节超过上限 %d", len(*body), flow.MaxComposeBodyBytes)
	}
	return nil
}

// validMethodToken 按 RFC 9110 的 token 规则校验方法名。方法直接写进请求行,
// 放一个空格进去就是第二段请求行。
func validMethodToken(m string) bool {
	if m == "" {
		return false
	}
	for _, c := range []byte(m) {
		if c <= ' ' || c >= 0x7f || strings.IndexByte("()<>@,;:\\\"/[]?={}", c) >= 0 {
			return false
		}
	}
	return true
}

// apply 将编辑增量写回 f，返回内容是否发生变化。
// Request/Response 采用就地字段更新，保留其原始线缆与响应元数据。
func (e *BreakpointEdit) apply(f *flow.Flow, phase flow.Phase) bool {
	if e == nil {
		return false
	}
	changed := false
	// 响应阶段仅应用响应编辑；请求已完成发送。
	if e.Request != nil && f.Request != nil && phase == flow.PhaseRequest {
		changed = e.Request.apply(f.Request) || changed
	}
	if e.Response != nil && f.Response != nil {
		changed = e.Response.apply(f.Response) || changed
	}
	return changed
}

func (e *RequestEdit) apply(r *flow.Request) bool {
	changed := false
	if e.Method != nil && *e.Method != r.Method {
		r.Method = *e.Method
		changed = true
	}

	// 头部先于 URL 应用；显式 Host 编辑优先，未编辑时由新 URL 主机名更新 Host。
	originalHost := r.Host
	if e.Headers != nil {
		// 头部字节已在校验前解析为最终值，这里直接应用有序头对。
		host, header, raw := splitEditedHeaders(e.Headers, r.Host)
		if host != r.Host {
			r.Host = host
			changed = true
		}
		if !sameHeaderMap(r.Header, header) || !sameHeaderPairs(r.RawHeaders, raw) {
			r.Header = header
			// RawHeaders 保持用户编辑后的写线顺序。
			r.RawHeaders = raw
			changed = true
		}
	}
	hostEdited := r.Host != originalHost

	if e.URL != nil && *e.URL != r.URL {
		r.URL = *e.URL
		// URL 已通过校验，Path 同步为 URL 的路径部分。
		if u, err := url.Parse(*e.URL); err == nil && u.Host != "" {
			if !hostEdited {
				r.Host = u.Host
			}
			r.Path = u.Path
		}
		changed = true
	}

	if e.Body != nil && *e.Body != string(r.Body) {
		r.Body = []byte(*e.Body)
		changed = true
	}
	return changed
}

func (e *ResponseEdit) apply(r *flow.Response) bool {
	changed := false
	if e.Status != nil && *e.Status != r.Status {
		r.Status = *e.Status
		// 未提供自定义状态文本时，按状态码生成标准文本。
		if e.StatusText == nil {
			r.StatusText = http.StatusText(*e.Status)
		}
		changed = true
	}
	if e.StatusText != nil && *e.StatusText != r.StatusText {
		r.StatusText = *e.StatusText
		changed = true
	}
	if e.Headers != nil {
		_, header, raw := splitEditedHeaders(e.Headers, "")
		if !sameHeaderMap(r.Header, header) || !sameHeaderPairs(r.RawHeaders, raw) {
			r.Header = header
			r.RawHeaders = raw
			changed = true
		}
	}
	if e.Body != nil && *e.Body != string(r.Body) {
		r.Body = []byte(*e.Body)
	// 编辑后的 body 是完整正文，清除截断标记并按新正文写线。
		r.ClearTruncated()
		changed = true
	}
	return changed
}

// splitEditedHeaders 将编辑后的有序头拆为 Host、规范化 map 与写线原始序列。
// 未提供 Host 时按 fallbackHost 补到首位；响应侧传空 fallbackHost 时保持原列表。
func splitEditedHeaders(pairs [][2]string, fallbackHost string) (string, map[string][]string, [][2]string) {
	host := fallbackHost
	hasHost := false
	header := make(map[string][]string, len(pairs))
	raw := make([][2]string, 0, len(pairs)+1)

	for _, kv := range pairs {
		name := strings.TrimSpace(kv[0])
		if name == "" {
			continue
		}
		raw = append(raw, [2]string{name, kv[1]})
		if textproto.CanonicalMIMEHeaderKey(name) == "Host" {
			hasHost = true
			if v := strings.TrimSpace(kv[1]); v != "" {
				host = v
			}
			continue
		}
		header[textproto.CanonicalMIMEHeaderKey(name)] = append(header[textproto.CanonicalMIMEHeaderKey(name)], kv[1])
	}

	// 空头列表交给标准请求头生成路径。
	if len(raw) == 0 {
		return host, header, nil
	}
	if !hasHost && fallbackHost != "" {
		raw = append([][2]string{{"Host", fallbackHost}}, raw...)
	}
	return host, header, raw
}

func sameHeaderMap(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
	}
	return true
}

func sameHeaderPairs(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
