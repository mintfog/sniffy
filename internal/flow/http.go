// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"io"
	"net/http"
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
	return f
}

// ApplyRequestToHTTP 把(可能被插件改过的)Flow.Request 写回 *http.Request,
// 以 identity 方式重建 body 并修正长度/编码头,供转发上游。
func ApplyRequestToHTTP(f *Flow, req *http.Request) error {
	r := f.Request
	if r == nil {
		return nil
	}

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
	req.Header.Del("Content-Encoding") // 以 identity 发送
	if keepTETrailers {
		req.Header.Set("TE", "trailers")
	}

	body := r.Body
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Length", itoa(len(body)))
	req.Host = r.Host
	req.RequestURI = "" // 出站请求必须清空
	return nil
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
