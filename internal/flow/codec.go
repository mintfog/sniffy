// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// 这些工具是 MITM 改写 body 正确性的核心(方案"风险 3"):
// 进入 Flow 时把 body 解码成 identity 字节,出站时统一重算 Content-Length、
// 去掉 Content-Encoding / chunked Transfer-Encoding / hop-by-hop 头。
// 它们从历史上的 web_api 插件迁移而来并补全了编码方向。

// hopByHopHeaders 是逐跳头,转发时必须剔除。
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// DecodeBody 按 contentEncoding 把 body 解压为 identity 字节。
// 返回解码后的字节以及是否真的发生了解码。无法识别的编码原样返回。
func DecodeBody(body []byte, contentEncoding string) ([]byte, bool) {
	if len(body) == 0 || contentEncoding == "" {
		return body, false
	}
	switch enc := strings.ToLower(strings.TrimSpace(contentEncoding)); {
	case strings.Contains(enc, "gzip"):
		return gunzip(body)
	case strings.Contains(enc, "deflate"):
		return inflate(body)
	case strings.Contains(enc, "zstd"):
		return unzstd(body)
	case strings.Contains(enc, "br"):
		// brotli:Google 等站点 HTTPS 默认压缩,不解码会让客户端把压缩字节当明文 → 乱码。
		return unbrotli(body)
	default:
		return body, false
	}
}

// EncodeBody 按 contentEncoding 对 identity 字节重新压缩。
// 仅支持 gzip/deflate;其它(含空)返回原字节且 ok=false。
func EncodeBody(body []byte, contentEncoding string) ([]byte, bool) {
	switch enc := strings.ToLower(strings.TrimSpace(contentEncoding)); {
	case strings.Contains(enc, "gzip"):
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		if _, err := w.Write(body); err != nil {
			return body, false
		}
		if err := w.Close(); err != nil {
			return body, false
		}
		return buf.Bytes(), true
	case strings.Contains(enc, "deflate"):
		var buf bytes.Buffer
		w, err := flate.NewWriter(&buf, flate.DefaultCompression)
		if err != nil {
			return body, false
		}
		if _, err := w.Write(body); err != nil {
			return body, false
		}
		if err := w.Close(); err != nil {
			return body, false
		}
		return buf.Bytes(), true
	default:
		return body, false
	}
}

func gunzip(body []byte) ([]byte, bool) {
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body, false
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return body, false
	}
	return out, true
}

func inflate(body []byte) ([]byte, bool) {
	r := flate.NewReader(bytes.NewReader(body))
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return body, false
	}
	return out, true
}

func unbrotli(body []byte) ([]byte, bool) {
	out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(body)))
	if err != nil {
		return body, false
	}
	return out, true
}

func unzstd(body []byte) ([]byte, bool) {
	r, err := zstd.NewReader(nil)
	if err != nil {
		return body, false
	}
	defer r.Close()
	out, err := r.DecodeAll(body, nil)
	if err != nil {
		return body, false
	}
	return out, true
}

// IsBinary 粗略判断字节是否为二进制(非打印字符比例 > 30%)。
func IsBinary(data []byte) bool {
	n := len(data)
	if n == 0 {
		return false
	}
	if n > 512 {
		n = 512
	}
	nonPrintable := 0
	for i := 0; i < n; i++ {
		b := data[i]
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			nonPrintable++
		}
	}
	return float64(nonPrintable)/float64(n) > 0.3
}

// BodyPreview 把 body 转成适合 UI/日志展示的字符串:
// 二进制或非 UTF-8 返回占位符,过长则截断。
func BodyPreview(body []byte, maxLen int) string {
	if len(body) == 0 {
		return ""
	}
	if IsBinary(body) || !utf8.Valid(body) {
		return ""
	}
	if maxLen > 0 && len(body) > maxLen {
		return string(body[:maxLen])
	}
	return string(body)
}

// FromHTTPHeader 把 http.Header 转成可序列化的 map[string][]string 副本。
func FromHTTPHeader(h http.Header) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// ToHTTPHeader 把 map[string][]string 写入一个 http.Header。
func ToHTTPHeader(m map[string][]string) http.Header {
	out := make(http.Header, len(m))
	for k, v := range m {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// StripHopByHop 从 header 中删除逐跳头。
func StripHopByHop(h http.Header) {
	for _, k := range hopByHopHeaders {
		h.Del(k)
	}
}
