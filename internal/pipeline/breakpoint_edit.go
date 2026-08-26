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

// ErrBreakpointNotFound 表示这条 flow 已不在暂停中(已放行 / 已阻断 / 已超时)。
// 调用方应据此告诉用户「这一条已经走了」,而不是当成一次失败的请求。
var ErrBreakpointNotFound = errors.New("断点不存在或已解除")

// BreakpointEdit 是 UI 放行时带回的编辑内容,patch 语义:字段缺省表示「没动过」,
// 不是「清空」。
//
// 刻意不复用 flow.Flow 当入参:UI 送回的 flow 经 JSON 解码后私有字段全为零,拿它整体
// 替换会把原始线缆字节、上游状态行、透传旁路的落盘副本一并丢掉,而「少传一个字段」
// 会静默变成「删掉它」—— 想只改一个 URL 也得把头、体、trailer 一字不差地搬一个来回。
//
// 头部用有序对而不是 map:线上本就是有序的,map 连「新加的头排在第几行」这种用户
// 亲眼看着排好的差异都表达不了。body 用 string 而不是 []byte:载不进编辑器的二进制体
// 直接不回传即可原样保留,不必为了不改它而搬几 MB 的 base64。
type BreakpointEdit struct {
	Request  *RequestEdit  `json:"request,omitempty"`
	Response *ResponseEdit `json:"response,omitempty"`
}

// RequestEdit 是请求侧的编辑。Headers 为 nil 表示不动;非 nil(含空列表)表示按它整体重设。
type RequestEdit struct {
	Method  *string     `json:"method,omitempty"`
	URL     *string     `json:"url,omitempty"`
	Headers [][2]string `json:"headers,omitempty"`
	// Body 是 identity 文本(不是线上的压缩字节):出站编码由 flow.ApplyRequestToHTTP
	// 按最终 body 重算,UI 改的永远是解码后的内容。
	Body *string `json:"body,omitempty"`
}

// ResponseEdit 是响应侧的编辑,字段语义同 RequestEdit。
type ResponseEdit struct {
	Status     *int        `json:"status,omitempty"`
	StatusText *string     `json:"statusText,omitempty"`
	Headers    [][2]string `json:"headers,omitempty"`
	Body       *string     `json:"body,omitempty"`
}

// Validate 在投递之前检查编辑内容。校验必须早于放行:不合法就把 flow 继续按在断点上,
// 用户改回来还能重来一次;放行之后再发现问题,那条请求已经出去了。
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

// validateAgainst 做「针对这一条 flow」的校验,补 Validate 只看形状看不到的那部分。
func (e *BreakpointEdit) validateAgainst(f *flow.Flow) error {
	if e == nil || e.Response == nil || e.Response.Status == nil || f == nil {
		return nil
	}
	// 流式与透传旁路的正文由上游逐条中继,写完头之后照发不误。这时把状态码改成无体码,
	// 线上就是一份「204 + Content-Length + 一大坨 body」的报文,客户端会把这些字节当成
	// 同一条连接上的下一个响应去解析,整条长连接从此错位。
	if bodylessStatus(*e.Response.Status) && relaysBodyVerbatim(f) {
		return fmt.Errorf("流式 / 透传响应的正文由上游原样中继,不能改成无体状态码 %d", *e.Response.Status)
	}
	return nil
}

// bodylessStatus 报告该状态码按 RFC 9110 不携带 body。
func bodylessStatus(code int) bool {
	return code == http.StatusNoContent || code == http.StatusNotModified || (code >= 100 && code < 200)
}

// relaysBodyVerbatim 报告这条 flow 的响应体是边收边发的(流式 / 透传旁路)。
// 标记由抓包侧在调 OnResponse 之前写入,断点看到的就是最终形态。
func relaysBodyVerbatim(f *flow.Flow) bool {
	if f.Metadata == nil {
		return false
	}
	_, streamed := f.Metadata["stream"]
	_, passthrough := f.Metadata["passthrough"]
	return streamed || passthrough
}

// validateHeaderEdit 拦下会破坏报文边界的头部。断点改过的头是原样写线的
// (见 flow.reconcileOrderedHeaders),含 CR/LF 就能拼出额外的头乃至第二个请求。
func validateHeaderEdit(pairs [][2]string) error {
	if pairs == nil {
		return nil
	}
	return flow.ValidateHeaderPairs(pairs)
}

// validateBodyEdit 与构造器共用同一条体积上限:能在断点里改出来的,构造器也发得出去。
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

// apply 把编辑增量写回 f,返回内容是否真的变了。
//
// 逐字段就地改写、绝不替换 Request/Response 指针:两者身上挂着不进 JSON 的原始线缆
// 形态(编码前的 body 字节、上游原始状态行、响应被截断的标记、透传旁路的落盘副本),
// 换指针会把它们连同保真回放一起清零 —— 与 internal/plugin/js 的 applyHTTP 同一个理由。
func (e *BreakpointEdit) apply(f *flow.Flow, phase flow.Phase) bool {
	if e == nil {
		return false
	}
	changed := false
	// 响应阶段的请求编辑一律忽略:请求早已发出,改它只会让存下来的会话与线上对不上。
	// 界面在这个阶段本就把请求侧置为只读,这里是给 REST 客户端的同一道闸。
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

	// 头部先于 URL 应用,两者都可能决定 Host,谁说了算要有个明确规则:
	// 用户手改过 Host 行就以 Host 行为准(改 Host 头是常见的定向测试手法),
	// 没改过则由新 URL 的主机名接管 —— 否则编辑器里预填的旧 Host 会把改过的 URL
	// 又拽回旧主机,请求带着旧 Host 发出去。
	originalHost := r.Host
	if e.Headers != nil {
		host, header, raw := splitEditedHeaders(e.Headers, r.Host)
		if host != r.Host {
			r.Host = host
			changed = true
		}
		if !sameHeaderMap(r.Header, header) || !sameHeaderPairs(r.RawHeaders, raw) {
			r.Header = header
			// 顺序也按用户排好的写线:编辑器里看到的次序若与线上不符,这个界面就在说谎。
			r.RawHeaders = raw
			changed = true
		}
	}
	hostEdited := r.Host != originalHost

	if e.URL != nil && *e.URL != r.URL {
		r.URL = *e.URL
		// Validate 已经确认过能解析且带主机名。Path 无条件跟随 URL:它只用于展示与规则
		// 匹配,留着旧值就是一条自相矛盾的记录。
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
		// 状态文本必须跟着换:出线时 reason 是从 StatusText 里剥掉状态码前缀得到的,
		// 留着上游那句就会拼出 "HTTP/1.1 404 200 OK"。UI 自己给了文本时以它为准。
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
		// 换过的 body 本身是完整的:留着截断标记会让出线沿用上游宣告的长度,
		// 客户端就去等一截永远不会来的字节。
		r.ClearTruncated()
		changed = true
	}
	return changed
}

// splitEditedHeaders 把编辑后的有序头拆成出站三件套:Host、供插件/规则/出站读值的
// 规范化 map,以及写线用的原始序列。
//
// 与 app.splitComposedHeaders 是同一件事的两处入口,规则也一致:Host 单独拎出来
// (net/http 的出站请求从 req.Host 取它,留在 map 里会被静默忽略),用户没写 Host 时
// 按 fallbackHost 补一条并置于首位 —— 交给 reconcileOrderedHeaders 兜底会被排到末尾,
// 拼出一份 Host 在最后一行的怪报文。响应侧传空 fallbackHost,不补 Host。
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

	// 一条头都不剩时不进保真写线路径,交给标准姿势拼,免得只剩一行 Host。
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
