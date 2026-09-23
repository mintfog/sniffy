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

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// SSEEvent 是以空行结尾的 SSE 块，包含数据事件、注释块和控制块。
type SSEEvent struct {
	Raw   []byte // 原始字节（含结尾空行），供未改写时原样转发
	Data  []byte // 按换行拼接的 data 字段；nil 表示无 data 字段，非 nil 空切片表示空数据事件
	Event string // event 字段值
	Type  string // 空值为数据事件，其余为 SSEComment / SSEControl
}

const (
	// SSEComment 标识仅含注释行的块，常用于心跳保活。
	SSEComment = "comment"
	// SSEControl 标识不含 data 且非纯注释的块，如 id、retry 或 event 字段块。
	SSEControl = "control"
)

// MaxSSEEventBytes 限制尚未遇到空行的 SSE 缓冲，避免上游持续发送不完整块耗尽内存。
// 每次 Push 切出完整块后检查剩余字节，超限后停止解析，见 SSEScanner.Overflowed。
const MaxSSEEventBytes = 8 << 20

// SSEScanner 增量解析 SSE 字节流。零值可用。
type SSEScanner struct {
	buf      []byte
	overflow bool
}

// Push 追加字节并返回以空行分隔的完整块，返回的字节切片独立于内部缓冲。
// 超限后仅缓冲后续输入；调用方须停止读取，或在每次 Push 后用 Flush 取走字节。
func (s *SSEScanner) Push(p []byte) []SSEEvent {
	s.buf = append(s.buf, p...)
	if s.overflow {
		return nil
	}
	var out []SSEEvent
	for {
		end := indexSSEBoundary(s.buf)
		if end < 0 {
			break
		}
		block := append([]byte(nil), s.buf[:end]...)
		s.buf = s.buf[end:]
		out = append(out, parseSSEBlock(block))
	}
	// 只检查未成块的尾部，避免把一次读入的多个完整块合计为单块大小。
	if len(s.buf) > MaxSSEEventBytes {
		s.overflow = true
	}
	return out
}

// Overflowed 报告是否因未成块缓冲超限而停止解析；该状态持续到扫描器被重置。
// 抓包侧将 Flush 返回的字节原样转发，构造器侧据此中止读取。
func (s *SSEScanner) Overflowed() bool { return s.overflow }

// Flush 交出当前缓冲的所有权并清空缓冲，保留 Overflowed 状态。
func (s *SSEScanner) Flush() []byte {
	b := s.buf
	s.buf = nil
	return b
}

// indexSSEBoundary 返回首个块的空行终止符之后的下标，未找到时返回 -1。
// 支持 LF、CRLF 及两者混用的换行。
func indexSSEBoundary(b []byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		j := i + 1
		if j < len(b) && b[j] == '\r' {
			j++
		}
		if j < len(b) && b[j] == '\n' {
			return j + 1
		}
	}
	return -1
}

func parseSSEBlock(block []byte) SSEEvent {
	ev := SSEEvent{Raw: block}
	var data []byte
	var hasComment, hasField bool
	for line := range bytes.SplitSeq(block, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			continue
		}
		if line[0] == ':' {
			hasComment = true
			continue
		}
		hasField = true
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			ev.Event = string(value)
		case "data":
			if data != nil {
				data = append(data, '\n')
			} else {
				data = []byte{}
			}
			data = append(data, value...)
		}
	}
	ev.Data = data
	if data == nil {
		ev.Type = SSEControl
		if hasComment && !hasField {
			ev.Type = SSEComment
		}
	}
	return ev
}

// ReserializeSSE 用事件名和载荷重建 SSE 字节，空载荷不生成 data 行。
// 重建结果仅含 event、data 字段；原块的 id、retry 和注释等内容不保留。
func ReserializeSSE(eventType string, data []byte) []byte {
	var b bytes.Buffer
	if eventType != "" {
		b.WriteString("event: ")
		b.WriteString(eventType)
		b.WriteByte('\n')
	}
	if len(data) > 0 {
		for _, line := range bytes.Split(data, []byte("\n")) {
			b.WriteString("data: ")
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// ContentTypeBase 返回去掉参数(; 之后)并小写的 Content-Type 主体。
func ContentTypeBase(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// IsEventStream 判断 Content-Type 是否为 SSE(容忍 charset 等参数)。
func IsEventStream(ct string) bool { return ContentTypeBase(ct) == "text/event-stream" }

// DecodeStreamBody 为流式响应按 Content-Encoding 包一层流式解码器(上游客户端 DisableCompression,
// 不会自动解压)。返回解码后的 reader 与「是否已消费 Content-Encoding」——后者为 true 时调用方应
// 删除响应的 Content-Encoding 头(body 已是 identity);无法识别的编码原样透传并保留该头(保真)。
func DecodeStreamBody(resp *http.Response) (io.Reader, bool) {
	ce := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	switch {
	case ce == "":
		return resp.Body, false // identity:无 Content-Encoding 头
	case strings.Contains(ce, "gzip"):
		if r, err := gzip.NewReader(resp.Body); err == nil {
			return r, true
		}
	case strings.Contains(ce, "deflate"):
		return flate.NewReader(resp.Body), true
	case strings.Contains(ce, "zstd"):
		if r, err := zstd.NewReader(resp.Body); err == nil {
			return r.IOReadCloser(), true
		}
	case strings.Contains(ce, "br"):
		return brotli.NewReader(resp.Body), true
	}
	return resp.Body, false // 未知/失败:原样透传压缩字节,保留 Content-Encoding 头
}
