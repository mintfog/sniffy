// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandoffRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ src, dst string }{
		{"172.29.113.1:50000", "203.0.113.80:443"},
		{"[fd73:6e69:6666::1]:1", "[2001:db8::80]:65535"},
		{"[::ffff:10.0.0.1]:80", "[::ffff:10.0.0.2]:8080"}, // IPv4 映射地址按 IPv4 传
	} {
		src, dst := netip.MustParseAddrPort(tc.src), netip.MustParseAddrPort(tc.dst)
		hdr := encodeHandoff(src, dst)
		if len(hdr) != handoffLen {
			t.Fatalf("头长 %d，应为 %d", len(hdr), handoffLen)
		}
		r := bytes.NewReader(append(hdr, "payload"...))
		gotSrc, gotDst, err := readHandoff(r)
		if err != nil {
			t.Fatalf("%s → %s: %v", tc.src, tc.dst, err)
		}
		wantSrc := netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
		wantDst := netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
		if gotSrc != wantSrc || gotDst != wantDst {
			t.Errorf("得到 %s → %s，应为 %s → %s", gotSrc, gotDst, wantSrc, wantDst)
		}
		if rest, _ := io.ReadAll(r); string(rest) != "payload" {
			t.Errorf("头之后的字节 %q，应原样保留", rest)
		}
	}
}

func TestHandoffSkipsOptions(t *testing.T) {
	t.Parallel()
	hdr := encodeHandoff(netip.MustParseAddrPort("10.0.0.1:1"), netip.MustParseAddrPort("10.0.0.2:2"))
	// 一个日后版本可能写入的字段：类型 1、长度 4、值。
	opts := []byte{1, 0, 4, 0, 0, 0x30, 0x39}
	binary.BigEndian.PutUint16(hdr[handoffLen-2:], uint16(len(opts)))
	r := bytes.NewReader(append(append(hdr, opts...), "data"...))
	if _, _, err := readHandoff(r); err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "data" {
		t.Errorf("跳过可选字段后剩下 %q，应为 data", rest)
	}
}

func TestHandoffRejectsMalformed(t *testing.T) {
	t.Parallel()
	valid := encodeHandoff(netip.MustParseAddrPort("10.0.0.1:1"), netip.MustParseAddrPort("10.0.0.2:2"))
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
		want   string
	}{
		{"魔数不对", func(b []byte) []byte { b[0] = 'x'; return b }, "不是 sniffy"},
		{"版本不符", func(b []byte) []byte { b[4] = 9; return b }, "版本"},
		{"控制连接", func(b []byte) []byte { b[5] = kindControl; return b }, "连接类型"},
		{"地址长度无效", func(b []byte) []byte { b[preambleLen] = 5; return b }, "地址长度"},
		{"可选字段区超长", func(b []byte) []byte {
			binary.BigEndian.PutUint16(b[handoffLen-2:], maxOptionLen+1)
			return b
		}, "超过上限"},
		{"可选字段区不完整", func(b []byte) []byte {
			binary.BigEndian.PutUint16(b[handoffLen-2:], 10)
			return append(b, 1, 2)
		}, "EOF"},
		{"头不完整", func(b []byte) []byte { return b[:handoffLen-1] }, "EOF"},
	} {
		b := tc.mutate(bytes.Clone(valid))
		_, _, err := readHandoff(bytes.NewReader(b))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: 错误 %v，应含 %q", tc.name, err, tc.want)
		}
	}
}

// unixPair 返回一对相连的 Unix socket。
func unixPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		c, _ := ln.AcceptUnix()
		accepted <- c
	}()
	a, err := net.DialUnix("unix", nil, ln.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	b := <-accepted
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

func TestControlMessages(t *testing.T) {
	t.Parallel()
	a, b := unixPair(t)
	ca, cb := &control{conn: a}, &control{conn: b}
	sent := []message{
		{Type: msgStart, Egress4: 3, Egress6: 7},
		{Type: msgStarted, Name: "sniffy0"},
		{Type: msgError, Error: "找不到 wintun.dll"},
		{Type: msgPing},
	}
	go func() {
		for _, m := range sent {
			_ = ca.send(m)
		}
		_ = a.CloseWrite()
	}()
	for _, want := range sent {
		got, err := cb.recv(time.Second)
		if err != nil || got != want {
			t.Fatalf("收到 %+v（%v），应为 %+v", got, err, want)
		}
	}
	if _, err := cb.recv(time.Second); err != io.EOF {
		t.Errorf("对端关闭写端后得到 %v，应为 io.EOF", err)
	}
}

func TestControlRecvLimits(t *testing.T) {
	t.Parallel()
	a, b := unixPair(t)
	cb := &control{conn: b}
	if _, err := cb.recv(50 * time.Millisecond); !isTimeout(err) {
		t.Errorf("没有消息时得到 %v，应超时", err)
	}
	_, _ = a.Write(binary.BigEndian.AppendUint32(nil, maxMsgLen+1))
	if _, err := cb.recv(time.Second); err == nil || !strings.Contains(err.Error(), "超过上限") {
		t.Errorf("超长消息得到 %v，应拒绝", err)
	}
}
