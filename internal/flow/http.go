// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

// 这些转换函数是 Flow 契约与 net/http 之间的桥,集中处理 MITM 改写 body 的正确性。
//
// 策略(v1,最简且正确):进入 Flow 时把 body 解码成 identity 字节;出站时一律
// 以 identity 重建——重算 Content-Length、删除 Content-Encoding 与逐跳头。
// 这样无论插件是否改了 body,客户端/上游收到的长度与编码都自洽。

const (
	metaReqEncoding  = "reqContentEncoding"
	metaRespEncoding = "respContentEncoding"
)

// BuildRequestFlow 从一个客户端 *http.Request 构造 Flow。
// 它会读尽并(按 Content-Encoding)解码请求体,然后用解码后的 body 重置 req.Body,
// 使调用方仍可正常转发。
func BuildRequestFlow(req *http.Request, protocol string) *Flow {
	f := New(protocol)

	var raw []byte
	if req.Body != nil {
		raw, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	ce := req.Header.Get("Content-Encoding")
	decoded, was := DecodeBody(raw, ce)
	if was {
		f.Metadata[metaReqEncoding] = ce
	}
	// 复位 req.Body,内容为 identity 字节(供未修改时直接转发)。
	req.Body = io.NopCloser(bytes.NewReader(decoded))

	f.Request = &Request{
		Method:   req.Method,
		URL:      req.URL.String(),
		Host:     req.Host,
		Path:     req.URL.Path,
		Proto:    req.Proto,
		Header:   FromHTTPHeader(req.Header),
		Body:     decoded,
		ClientIP: req.RemoteAddr,
	}
	// 读取侧抓到的原始头序列(顺序+大小写),供出站时按原样回放。h2/超大头时为空。
	if rawHdr, ok := RawHeadersFrom(req.Context()); ok {
		f.Request.RawHeaders = rawHdr
	}
	// 记录请求体原始线缆形态:仅当客户端确实带了 Content-Encoding 时,出站可在 body
	// 未被改动的前提下按原样回放编码字节(保真),避免重编码成 identity 破坏 body 指纹。
	if ce != "" && len(raw) > 0 {
		f.Request.SetOriginalBody(raw, decoded, ce)
	}
	return f
}

// ApplyRequestToHTTP 把(可能被插件改过的)Flow.Request 写回 *http.Request,
// 以 identity 方式重建 body 并修正长度/编码头,供转发上游。
//
// 返回最终要发送的 *http.Request:当读取侧抓到了原始头序列(HTTP/1.x)时,会把
// 「最终线缆头序列」经 ctx 附在请求上,供保真转发器(internal/forward)按原样写线
// (顺序+大小写保真);其余情形(h2 入站、合成请求)退化为标准 net/http 行为。
// 因 ctx 的写入会复制 *http.Request,调用方必须改用返回的请求。
func ApplyRequestToHTTP(f *Flow, req *http.Request) *http.Request {
	r := f.Request
	if r == nil {
		return req
	}

	// 装入响应头收集器:保真转发器读到上游响应头时回填原始状态行/头序列,
	// 供 CaptureResponseToFlow 取用、写回客户端时保真(顺序+大小写+状态行)。
	req = req.WithContext(WithResponseCapture(req.Context(), &ResponseCapture{}))

	req.Method = r.Method
	// URL 可能被改写。
	if r.URL != "" && r.URL != req.URL.String() {
		if u, err := req.URL.Parse(r.URL); err == nil {
			req.URL = u
		}
	}

	// 重写 header。gRPC 要求请求携带 `TE: trailers`(h2 下 TE 唯一合法值),否则严格的
	// gRPC 源站会拒绝。TE 是逐跳头、会被 StripHopByHop 删除,故先探测再删后按需补回,
	// 避免破坏 gRPC-over-h2 的转发(Go 的 h2 客户端不会自行补 te)。
	req.Header = ToHTTPHeader(r.Header)
	keepTETrailers := hasToken(req.Header.Values("TE"), "trailers")
	StripHopByHop(req.Header)

	// 出站 body 与 Content-Encoding:插件未改动 body 且客户端原带编码 → 按原样回放编码后的
	// 线缆字节并保留 Content-Encoding(保真);否则以 identity 重建并删除 Content-Encoding。
	body := r.Body
	if enc := r.OriginalEncodedBody(r.Body); enc != nil {
		body = enc
		req.Header.Set("Content-Encoding", r.origEncoding)
	} else {
		req.Header.Del("Content-Encoding")
	}
	if keepTETrailers {
		req.Header.Set("TE", "trailers")
	}

	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Host = r.Host
	req.RequestURI = "" // 出站请求必须清空

	// 探测客户端原始是否带过 Content-Length / User-Agent(决定出站是否合成这两个头)。
	clientHadCL, clientHadUA := false, false
	for _, kv := range r.RawHeaders {
		switch textproto.CanonicalMIMEHeaderKey(kv[0]) {
		case "Content-Length":
			clientHadCL = true
		case "User-Agent":
			clientHadUA = true
		}
	}

	// 仅在确有 body 或客户端原本就带 Content-Length 时才发送它:
	// 避免给无 body 的 GET 等凭空加上 Content-Length: 0 这类侵入。
	if len(body) > 0 || clientHadCL {
		req.Header.Set("Content-Length", itoa(len(body)))
	} else {
		req.Header.Del("Content-Length")
	}

	// 计算最终线缆头序列(保真路径用):以原始顺序/大小写为骨架,回填当前头值。
	if len(r.RawHeaders) > 0 {
		vals := cloneHTTPHeader(req.Header)
		if r.Host != "" {
			// http.ReadRequest 把 Host 从 Header 挪到 req.Host;回放时需补回到原始位置。
			vals["Host"] = []string{r.Host}
		}
		ordered := reconcileOrderedHeaders(r.RawHeaders, vals)
		req = req.WithContext(WithOrderedHeaders(req.Context(), ordered))

		// 回退路径忠实性:客户端原本没带 UA 时,用空值哨兵阻止 net/http 注入
		// Go-http-client/1.1(置空键会让 Request.write 整行省略)。保真路径不读此哨兵。
		if !clientHadUA {
			req.Header["User-Agent"] = []string{""}
		}
	}
	return req
}

// CaptureResponseToFlow 读尽并解码 *http.Response 的 body,填入 Flow.Response。
// 同时用解码后的 body 复位 resp.Body。
func CaptureResponseToFlow(f *Flow, resp *http.Response) {
	var raw []byte
	if resp.Body != nil {
		raw, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	ce := resp.Header.Get("Content-Encoding")
	decoded, was := DecodeBody(raw, ce)
	if was {
		f.Metadata[metaRespEncoding] = ce
	}
	resp.Body = io.NopCloser(bytes.NewReader(decoded))

	f.Response = &Response{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Header:     FromHTTPHeader(resp.Header),
		Body:       decoded,
		// resp.Trailer 在 body 读尽(上面的 io.ReadAll)后才被 net/http 填充;
		// 无尾部时为 nil,经 omitempty 省略。gRPC 的 grpc-status 即在此。
		Trailer: FromHTTPHeader(resp.Trailer),
	}

	// 保真写回客户端所需:上游响应原始状态行/头序列(由转发器经 ctx 回填),
	// 以及原始编码体(供 body 未改动时原样回放)。回退 / h2 / mock 时收集器为空。
	if resp.Request != nil {
		if rc, ok := ResponseCaptureFrom(resp.Request.Context()); ok && len(rc.Headers) > 0 {
			f.Response.RawHeaders = rc.Headers
			f.Response.SetOriginalHead(rc.StatusLine)
			if ce != "" && len(raw) > 0 {
				f.Response.SetOriginalBody(raw, decoded, ce)
			}
		}
	}
}

// BuildHTTPResponse 从 Flow.Response 构造一个可写回客户端的 *http.Response。
// 用于正常转发(应用插件修改)与 mock(无上游)两种场景,均以 identity 重建。
func BuildHTTPResponse(f *Flow, req *http.Request) *http.Response {
	r := f.Response
	if r == nil {
		r = &Response{Status: http.StatusOK, Header: map[string][]string{}}
	}
	header := ToHTTPHeader(r.Header)
	StripHopByHop(header)
	header.Del("Content-Encoding")

	body := r.Body
	header.Set("Content-Length", itoa(len(body)))

	status := r.Status
	if status == 0 {
		status = http.StatusOK
	}
	statusText := r.StatusText
	if statusText == "" {
		statusText = http.StatusText(status)
	}

	return &http.Response{
		Status:        statusText,
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// WriteResponse 把 Flow.Response 写回客户端 w。
// 当转发器捕获到上游响应的原始头序列(HTTP/1.x)时,按原始状态行/顺序/大小写/编码逐字回放
// (响应保真);否则退化为标准 *http.Response.Write(identity 重建 + 字母序规范化头)。
// req 用于判定 HEAD 等无体响应。
func WriteResponse(w io.Writer, f *Flow, req *http.Request) error {
	method := ""
	if req != nil {
		method = req.Method
	}
	if r := f.Response; r != nil && len(r.RawHeaders) > 0 {
		return writeFaithfulResponse(w, r, method)
	}
	return BuildHTTPResponse(f, req).Write(w)
}

// writeFaithfulResponse 按上游原始状态行/头序列/大小写写回响应。
// 帧头(Content-Encoding / Content-Length / Transfer-Encoding)据实际 body 自洽化:
// body 未改动且原带编码 → 原样回放编码字节并保留 Content-Encoding;否则 identity 重建。
// 逐跳头(Connection / Transfer-Encoding 等)按代理语义剔除。
func writeFaithfulResponse(w io.Writer, r *Response, method string) error {
	body := r.Body
	keepCE := false
	if enc := r.OriginalEncodedBody(r.Body); enc != nil {
		body = enc
		keepCE = true
	}

	vals := ToHTTPHeader(r.Header)
	StripHopByHop(vals) // 去 Connection / Transfer-Encoding / Keep-Alive 等逐跳头
	if keepCE {
		vals.Set("Content-Encoding", r.origEncoding)
	} else {
		vals.Del("Content-Encoding")
	}

	code := r.Status
	if code == 0 {
		code = http.StatusOK
	}
	bodyless := bodylessResponse(code, method)
	if bodyless {
		// 无体响应(HEAD / 204 / 304 / 1xx):不写 body,Content-Length 沿用上游原值(若有)。
		if cl := rawHeaderValue(r.RawHeaders, "Content-Length"); cl != "" {
			vals.Set("Content-Length", cl)
		}
	} else {
		vals.Set("Content-Length", strconv.Itoa(len(body)))
	}

	ordered := reconcileOrderedHeaders(r.RawHeaders, vals)

	var head bytes.Buffer
	head.WriteString(responseStatusLine(r, code))
	head.WriteString("\r\n")
	for _, kv := range ordered {
		head.WriteString(kv[0])
		head.WriteString(": ")
		head.WriteString(kv[1])
		head.WriteString("\r\n")
	}
	head.WriteString("\r\n")
	if _, err := w.Write(head.Bytes()); err != nil {
		return err
	}
	if !bodyless && len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return nil
}

// responseStatusLine 生成写回客户端的状态行:状态码未被改动时逐字回放上游原始状态行,
// 否则以上游协议版本重建 "<proto> <code> <reason>"。
func responseStatusLine(r *Response, code int) string {
	if r.origStatusLine != "" && statusCodeOf(r.origStatusLine) == code {
		return r.origStatusLine
	}
	proto := protoOf(r.origStatusLine)
	if proto == "" {
		proto = "HTTP/1.1"
	}
	reason := strings.TrimSpace(strings.TrimPrefix(r.StatusText, strconv.Itoa(code)))
	if reason == "" {
		reason = http.StatusText(code)
	}
	return proto + " " + strconv.Itoa(code) + " " + reason
}

// bodylessResponse 报告该响应按 RFC 不应携带 body(故不写 body、不据 body 重算长度)。
func bodylessResponse(status int, method string) bool {
	return method == http.MethodHead || status == http.StatusNoContent ||
		status == http.StatusNotModified || (status >= 100 && status < 200)
}

// rawHeaderValue 返回 raw 中首个匹配(规范名)的头值;无则空串。
func rawHeaderValue(raw [][2]string, name string) string {
	want := textproto.CanonicalMIMEHeaderKey(name)
	for _, kv := range raw {
		if textproto.CanonicalMIMEHeaderKey(kv[0]) == want {
			return kv[1]
		}
	}
	return ""
}

// protoOf 取状态行的协议版本(首个 token),如 "HTTP/1.1"。
func protoOf(statusLine string) string {
	if i := strings.IndexByte(statusLine, ' '); i > 0 {
		return statusLine[:i]
	}
	return ""
}

// statusCodeOf 取状态行的状态码(第二个 token);解析失败返回 0。
func statusCodeOf(statusLine string) int {
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		return 0
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return n
}

// itoa 避免为单处转换引入 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
