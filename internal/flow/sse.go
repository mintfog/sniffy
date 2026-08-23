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

// SSEEvent 是一条解析出的 SSE 事件:Raw 为原始字节块(含结尾空行,供未改动时保真回放),
// Data 为按规范拼接的 data 字段载荷,Event 为 event 字段名。
type SSEEvent struct {
	Raw   []byte
	Data  []byte
	Event string
}

// MaxSSEEventBytes 是单个 SSE 事件块的字节上限。事件的边界是空行,上游只要一直不发空行,
// 缓冲就一直涨 —— 内存用量由对端说了算。超过它只有两种可能:畸形的巨型事件,或者根本
// 不在说 SSE;两种情况下继续攒都没有意义,故转为 overflow(见 SSEScanner.Overflowed)。
const MaxSSEEventBytes = 8 << 20

// SSEScanner 增量解析 SSE 字节流。零值可用。
//
// 抓包侧的响应中继与构造器发起的出站 SSE 共用它:两边必须按同一套边界规则切事件,
// 否则同一条流在「抓到的」与「构造的」两个界面里会显示成不同的事件序列。
type SSEScanner struct {
	buf      []byte
	overflow bool // 单事件超限:停止逐事件解析,交由调用方处置(与 grpcScanner 同构)
}

// Push 追加字节并返回其中已完整的事件块(以空行分隔)。
// 一旦转入 overflow 就不再返回事件,调用方必须每次都把 Flush 的字节取走,否则缓冲照涨。
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
	// 切完之后剩的才是「尚未成块的尾巴」;超限判定必须在这之后做,
	// 否则一次读进来的多个完整事件会被误判成一个超大事件。
	if len(s.buf) > MaxSSEEventBytes {
		s.overflow = true
	}
	return out
}

// Overflowed 报告是否已因单事件超限转入透传模式。
// 抓包侧据此把 Flush 的字节原样中继给下游客户端(不能吞,否则响应就被改坏了);
// 构造器侧没有下游可喂,据此中止这条流。
func (s *SSEScanner) Overflowed() bool { return s.overflow }

// Flush 返回结尾未成块的残留字节(EOF 时原样透传),并清空内部缓冲。
func (s *SSEScanner) Flush() []byte {
	b := s.buf
	s.buf = nil
	return b
}

// indexSSEBoundary 返回首个事件块结束后的下标(即空行终止符之后),未结束返回 -1。
// 空行 = 连续两个换行(容忍 \n\n、\r\n\r\n 及混用)。
func indexSSEBoundary(b []byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		// b[i] 是一个换行;看它是否紧跟「空行」(即下一行为空)。
		j := i + 1
		if j < len(b) && b[j] == '\r' {
			j++
		}
		if j < len(b) && b[j] == '\n' {
			return j + 1 // 含整个空行终止符
		}
	}
	return -1
}

// parseSSEBlock 解析一个 SSE 事件块,提取 event 名与拼接后的 data 载荷。
func parseSSEBlock(block []byte) SSEEvent {
	ev := SSEEvent{Raw: block}
	var data []byte
	for _, line := range bytes.Split(block, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 || line[0] == ':' {
			continue // 空行 / 注释行
		}
		field, value := line, []byte(nil)
		if c := bytes.IndexByte(line, ':'); c >= 0 {
			field = line[:c]
			value = line[c+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}
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
	return ev
}

// ReserializeSSE 在事件载荷被改动后重建一个 SSE 事件块(保留 event 名)。
// 注:id/retry 等字段不保留(改写场景罕见,且改写方拿到的是 data 载荷而非原文)。
func ReserializeSSE(eventType string, data []byte) []byte {
	var b bytes.Buffer
	if eventType != "" {
		b.WriteString("event: ")
		b.WriteString(eventType)
		b.WriteByte('\n')
	}
	// 仅在确有载荷时写 data 行,避免空载荷被重建成多余的 "data: "(改变原事件语义)。
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
