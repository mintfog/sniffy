// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bytes"
	"errors"
	"io"
	"sync/atomic"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// h2SniffLimit 是为解析首个请求最多读取的字节数，单帧声明的长度也不能超过它。首个请求之前
// 只有连接前言与几个设置帧，头块超过这个长度的请求极少见；超出后放弃解析，按发给源站的请求处理。
const h2SniffLimit = 64 << 10

const h2FrameHeaderLen = 9

// h2FirstRequest 从客户端发来的字节流里旁路解析出连接上第一个请求的 :scheme。
//
// http2 服务端不把 :scheme 交给 handler，而判断「原始目标是 HTTPS 代理」要用到它，
// 只能从线上字节取。只看第一个请求：解析后续请求要一直跟着解码每个头块以维护 HPACK
// 动态表，而一条连接上各请求的 :scheme 由连接的用途决定，经代理发 http 请求的连接上都是 http。
//
// 字节到达的批次由对端决定，可能每次只有一个字节，所以每次送入都从上次停下的帧接着处理：
// 收齐的非 HEADERS 帧随即丢弃，首个请求的头块收齐后才解码一次。
//
// feed 只在连接的读方向上调用，scheme 可在任意 goroutine 调用。
type h2FirstRequest struct {
	// buf 是尚未处理完的字节：没读全的连接前言、没收齐的帧，或首个请求头块已到的各帧。
	buf         []byte
	received    int
	prefaceRead bool
	// blockEnd 是 buf 里头块已收齐各帧的结尾，为 0 表示还没收齐过首个请求的 HEADERS 帧。
	blockEnd int
	result   atomic.Pointer[string]
}

// feed 送入刚从连接读到的字节，返回 true 表示解析已结束、不必再送。
func (f *h2FirstRequest) feed(p []byte) (done bool) {
	f.received += len(p)
	f.buf = append(f.buf, p...)
	scheme, err := f.parse()
	if errors.Is(err, io.ErrUnexpectedEOF) && f.received < h2SniffLimit {
		return false
	}
	if err == nil {
		f.result.Store(new(scheme))
	}
	f.buf = nil
	return true
}

// scheme 返回第一个请求的 :scheme（CONNECT 请求没有，为空），尚未读到或解析不出时 ok 为
// false。http2 服务端要先读完请求的头块才会调用 handler，handler 里取到的至少是它自己这个请求。
func (f *h2FirstRequest) scheme() (scheme string, ok bool) {
	if f == nil {
		return "", false
	}
	if p := f.result.Load(); p != nil {
		return *p, true
	}
	return "", false
}

// parse 从上次停下的地方接着处理 buf。数据还不够时返回 io.ErrUnexpectedEOF。
func (f *h2FirstRequest) parse() (string, error) {
	if !f.prefaceRead {
		if err := f.readPreface(); err != nil {
			return "", err
		}
	}
	for {
		off := f.blockEnd
		if len(f.buf)-off < h2FrameHeaderLen {
			return "", io.ErrUnexpectedEOF
		}
		h := f.buf[off:]
		length := int(h[0])<<16 | int(h[1])<<8 | int(h[2])
		typ, flags := http2.FrameType(h[3]), http2.Flags(h[4])
		if length > h2SniffLimit {
			return "", errors.New("帧长超过解析上限")
		}
		end := off + h2FrameHeaderLen + length
		if len(f.buf) < end {
			return "", io.ErrUnexpectedEOF
		}
		switch {
		case off == 0 && typ != http2.FrameHeaders:
			f.buf = f.buf[end:]
		case off > 0 && typ != http2.FrameContinuation:
			return "", errors.New("头块之后不是 CONTINUATION 帧")
		// HEADERS 与 CONTINUATION 帧的 END_HEADERS 是同一个标志位。
		case flags.Has(http2.FlagHeadersEndHeaders):
			return decodeH2Scheme(f.buf[:end])
		default:
			f.blockEnd = end
		}
	}
}

func (f *h2FirstRequest) readPreface() error {
	n := min(len(f.buf), len(http2.ClientPreface))
	if string(f.buf[:n]) != http2.ClientPreface[:n] {
		return errors.New("不是 HTTP/2 连接前言")
	}
	if n < len(http2.ClientPreface) {
		return io.ErrUnexpectedEOF
	}
	f.buf = f.buf[n:]
	f.prefaceRead = true
	return nil
}

// decodeH2Scheme 从一个完整头块（HEADERS 帧及其后的 CONTINUATION 帧）里解码出 :scheme。
func decodeH2Scheme(block []byte) (string, error) {
	framer := http2.NewFramer(io.Discard, bytes.NewReader(block))
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	frame, err := framer.ReadFrame()
	if err != nil {
		return "", err
	}
	headers, ok := frame.(*http2.MetaHeadersFrame)
	if !ok {
		return "", errors.New("不是 HEADERS 帧")
	}
	return headers.PseudoValue("scheme"), nil
}
