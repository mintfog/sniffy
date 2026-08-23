// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// 这些工具是 MITM 改写 body 正确性的核心:
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

// ErrBodyTooLarge 表示解码后的消息体超过调用方给定的上限,解码已中止、返回的字节是截断的。
//
// 上限必须落在解码之后:压缩体的线上字节数与解码后的字节数能差三个数量级,只卡传输字节
// 拦不住压缩炸弹 —— 半兆的 gzip 就能展开成 512 MiB 并整块留在 Flow.Body 里。
var ErrBodyTooLarge = errors.New("解码后的消息体超过上限")

// DecodeBody 按 contentEncoding 把 body 解压为 identity 字节。
// 返回解码后的字节以及是否真的发生了解码。无法识别的编码原样返回。
func DecodeBody(body []byte, contentEncoding string) ([]byte, bool) {
	decoded, was, _ := DecodeBodyLimit(body, contentEncoding, 0)
	return decoded, was
}

// DecodeBodyLimit 与 DecodeBody 同义,但解码结果超过 limit 字节即中止,返回截断的字节与
// 包装了 ErrBodyTooLarge 的错误(limit <= 0 表示不设限)。
//
// 解码失败(编码对不上、数据损坏)的处置与 DecodeBody 一致:原样返回且不报错 ——
// 认不出的编码不是错误,把线上字节交给上层展示比丢弃更有用。
func DecodeBodyLimit(body []byte, contentEncoding string, limit int64) ([]byte, bool, error) {
	if len(body) == 0 || contentEncoding == "" {
		return body, false, nil
	}
	switch enc := strings.ToLower(strings.TrimSpace(contentEncoding)); {
	case strings.Contains(enc, "gzip"):
		return decodeStream(body, limit, gzipReader)
	case strings.Contains(enc, "deflate"):
		return decodeStream(body, limit, flateReader)
	case strings.Contains(enc, "zstd"):
		return decodeStream(body, limit, zstdReader)
	case strings.Contains(enc, "br"):
		// brotli:Google 等站点 HTTPS 默认压缩,不解码会让客户端把压缩字节当明文 → 乱码。
		return decodeStream(body, limit, brotliReader)
	default:
		return body, false, nil
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

// decoderFactory 建一个读 body 的解码器,并交回释放它所需的收尾函数。
// 收尾单列而不用 io.Closer:zstd 的 Close 没有返回值,并不满足这个接口。
type decoderFactory func(io.Reader) (io.Reader, func(), error)

// decodeStream 用 newReader 建解码器读尽 body,并把结果卡在 limit 字节内。
func decodeStream(body []byte, limit int64, newReader decoderFactory) ([]byte, bool, error) {
	r, cleanup, err := newReader(bytes.NewReader(body))
	if err != nil {
		return body, false, nil
	}
	defer cleanup()
	out, err := readAllLimit(r, limit)
	if errors.Is(err, ErrBodyTooLarge) {
		return out, true, err
	}
	if err != nil {
		return body, false, nil
	}
	return out, true, nil
}

// readAllLimit 读尽 r,但最多接受 limit 字节(limit <= 0 不设限)。
//
// 不能只靠 io.LimitReader 收口:它到顶只给 EOF,调用方会把一份腰斩的结果当成解码完成 ——
// 界面上是绿的、内容却少了一截,正是这条路径最不能出的错(与 app.cappedBody 同理)。
func readAllLimit(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(r)
	}
	// 多读一字节:只用来把「恰好等于上限」和「超了」分开。
	out, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return out, err
	}
	if int64(len(out)) > limit {
		return out[:limit], fmt.Errorf("%w %d 字节", ErrBodyTooLarge, limit)
	}
	return out, nil
}

func gzipReader(r io.Reader) (io.Reader, func(), error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, func() {}, err
	}
	return zr, func() { _ = zr.Close() }, nil
}

func flateReader(r io.Reader) (io.Reader, func(), error) {
	fr := flate.NewReader(r)
	return fr, func() { _ = fr.Close() }, nil
}

func brotliReader(r io.Reader) (io.Reader, func(), error) {
	return brotli.NewReader(r), func() {}, nil
}

// zstdReader 用流式解码器而非 DecodeAll:后者一次性把整个结果分配出来,limit 插不进去。
func zstdReader(r io.Reader) (io.Reader, func(), error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return nil, func() {}, err
	}
	// *zstd.Decoder 持有 goroutine,读没读尽都要释放。
	return zr, zr.Close, nil
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

// hasToken 报告 values 中是否(不区分大小写)包含给定的(小写)token。
func hasToken(values []string, lowerToken string) bool {
	for _, v := range values {
		if strings.Contains(strings.ToLower(v), lowerToken) {
			return true
		}
	}
	return false
}
