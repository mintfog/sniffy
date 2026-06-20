// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"bytes"
	"net/http"
	"strings"

	"github.com/mintfog/sniffy/internal/flow"
)

// 读取侧头部保真:http.ReadRequest 会把头名规范化、丢掉顺序(Header 是 map),
// 一旦解析就无从恢复。故在 ReadRequest 之前先 Peek 出原始头块,按线上顺序与
// 原始大小写解析成 [][2]string,经 ctx 挂到请求上;BuildRequestFlow 再存入 Flow。
// Peek 不推进读取指针,ReadRequest 随后照常解析同一 reader。

// maxHeaderPeek 是头部保真可 Peek 的上限(需 <= 连接 reader 缓冲大小)。
// 超过则放弃保真、回退标准转发(极少见)。
const maxHeaderPeek = 64 * 1024

// readRequestPreservingOrder 在解析前抓取原始头序列,再正常 ReadRequest,
// 并把抓到的原始头序列经 ctx 挂到返回的请求上(抓取失败时不挂,自动回退)。
func readRequestPreservingOrder(br *bufio.Reader) (*http.Request, error) {
	raw := peekRequestHeaderOrder(br)
	req, err := http.ReadRequest(br)
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		req = req.WithContext(flow.WithRawHeaders(req.Context(), raw))
	}
	return req, nil
}

// peekRequestHeaderOrder 增量 Peek 出完整头块(直到 CRLFCRLF),解析为原始头序列。
// 只在确实需要更多字节时阻塞读取 1 字节,故对「无 body 的小请求」也不会死等。
// 头块超出缓冲或连接出错时返回 nil(放弃保真)。
func peekRequestHeaderOrder(br *bufio.Reader) [][2]string {
	for {
		if n := br.Buffered(); n > 0 {
			b, _ := br.Peek(n)
			if end := headerBlockEnd(b); end >= 0 {
				return parseRequestHeaderLines(b[:end])
			}
			if n >= maxHeaderPeek {
				return nil // 头块过大,放弃保真
			}
		}
		// 缓冲内尚无完整头块,阻塞读取至少再 1 字节。
		if _, err := br.Peek(br.Buffered() + 1); err != nil {
			return nil // EOF / 缓冲装满
		}
	}
}

// headerBlockEnd 返回头块结束位置:即 CRLFCRLF 中第一个 CRLF 之后的下标
// (使返回切片含最后一个头的尾随 CRLF、但不含终止空行)。未结束返回 -1。
func headerBlockEnd(b []byte) int {
	if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
		return i + 2
	}
	// 容忍极少数仅用 LF 的实现。
	if i := bytes.Index(b, []byte("\n\n")); i >= 0 {
		return i + 1
	}
	return -1
}

// parseRequestHeaderLines 把头块文本解析成原始头序列(保留顺序与原始大小写,含重复头)。
// 跳过请求行;对 obs-fold 续行并入上一个值。
func parseRequestHeaderLines(head []byte) [][2]string {
	lines := strings.Split(string(head), "\n")
	out := make([][2]string, 0, len(lines))
	first := true
	for _, ln := range lines {
		ln = strings.TrimSuffix(ln, "\r")
		if ln == "" {
			continue
		}
		if first {
			first = false // 请求行
			continue
		}
		if ln[0] == ' ' || ln[0] == '\t' {
			if len(out) > 0 {
				out[len(out)-1][1] += " " + strings.TrimSpace(ln)
			}
			continue
		}
		c := strings.IndexByte(ln, ':')
		if c < 0 {
			continue
		}
		name := ln[:c]
		val := strings.TrimLeft(ln[c+1:], " \t")
		out = append(out, [2]string{name, val})
	}
	return out
}
