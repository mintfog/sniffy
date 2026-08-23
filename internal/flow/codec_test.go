// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	original := []byte("hello sniffy, this is a body that will be compressed and decompressed")

	for _, enc := range []string{"gzip", "deflate"} {
		encoded, ok := EncodeBody(original, enc)
		if !ok {
			t.Fatalf("EncodeBody(%s) reported failure", enc)
		}
		if bytes.Equal(encoded, original) {
			t.Fatalf("EncodeBody(%s) did not change bytes", enc)
		}
		decoded, ok := DecodeBody(encoded, enc)
		if !ok {
			t.Fatalf("DecodeBody(%s) reported failure", enc)
		}
		if !bytes.Equal(decoded, original) {
			t.Fatalf("round trip(%s) mismatch: got %q want %q", enc, decoded, original)
		}
	}
}

func TestDecodeBodyUnknownEncoding(t *testing.T) {
	body := []byte("plain")
	out, ok := DecodeBody(body, "snappy")
	if ok {
		t.Fatalf("expected unsupported encoding snappy to report ok=false")
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("unsupported encoding should return original bytes")
	}
}

// TestDecodeBrotli 确认 br 响应被正确解码为明文(此前不支持会导致客户端乱码)。
func TestDecodeBrotli(t *testing.T) {
	original := []byte("hello brotli — 谷歌 HTTPS 默认用它压缩,必须解码否则乱码")

	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write(original); err != nil {
		t.Fatalf("brotli write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("brotli close: %v", err)
	}
	if buf.Len() == 0 || bytes.Equal(buf.Bytes(), original) {
		t.Fatalf("brotli 未真正压缩")
	}

	decoded, ok := DecodeBody(buf.Bytes(), "br")
	if !ok {
		t.Fatalf("DecodeBody(br) 报告失败")
	}
	if !bytes.Equal(decoded, original) {
		t.Fatalf("brotli 解码不一致: got %q want %q", decoded, original)
	}
}

// TestDecodeZstd 确认 zstd 响应被正确解码为明文。
func TestDecodeZstd(t *testing.T) {
	original := []byte("hello zstd, a body compressed with zstandard for testing")

	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := w.Write(original); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}

	decoded, ok := DecodeBody(buf.Bytes(), "zstd")
	if !ok {
		t.Fatalf("DecodeBody(zstd) 报告失败")
	}
	if !bytes.Equal(decoded, original) {
		t.Fatalf("zstd 解码不一致: got %q want %q", decoded, original)
	}
}

func TestIsBinary(t *testing.T) {
	if IsBinary([]byte("a normal utf-8 string")) {
		t.Fatalf("text wrongly classified as binary")
	}
	if !IsBinary([]byte{0x00, 0x01, 0x02, 0x03, 0x00, 0x01, 0xff, 0xfe}) {
		t.Fatalf("binary wrongly classified as text")
	}
}

func TestDecisionMergePrecedence(t *testing.T) {
	acc := ContinueDecision()
	acc = Merge(acc, BreakpointDecision(PhaseRequest, ""))
	if acc.Kind != Breakpoint {
		t.Fatalf("breakpoint should beat continue")
	}
	acc = Merge(acc, MockDecision(""))
	if acc.Kind != Mock {
		t.Fatalf("mock should beat breakpoint")
	}
	acc = Merge(acc, AbortDecision(403, ""))
	if acc.Kind != Abort {
		t.Fatalf("abort should beat mock")
	}
	// 较低优先级不应覆盖。
	acc = Merge(acc, ContinueDecision())
	if acc.Kind != Abort {
		t.Fatalf("continue should not override abort")
	}
}

// gzipOf 把 plain 压成 gzip 字节。
func gzipOf(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 压缩体的线上字节数与解码后的字节数能差三个数量级:调用方按传输字节设的上限
// 对压缩响应形同虚设,上限必须落在解码这一层。
func TestDecodeBodyLimitStopsCompressionBomb(t *testing.T) {
	const limit = 64 << 10
	gz := gzipOf(t, bytes.Repeat([]byte("x"), 8<<20))
	if int64(len(gz)) >= limit {
		t.Fatalf("压缩后 %d 字节已超过上限 %d,这个用例就测不到「传输没超、解出来超了」", len(gz), limit)
	}

	out, was, err := DecodeBodyLimit(gz, "gzip", limit)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v,期望 ErrBodyTooLarge", err)
	}
	if !was {
		t.Fatal("确实解了码,was 不该为假 —— 否则调用方会把截断的字节当成原始压缩字节")
	}
	if int64(len(out)) > limit {
		t.Fatalf("交回 %d 字节,超过上限 %d", len(out), limit)
	}
}

// 差一字节就把正常内容判成超限,是这类上限最常见的错法。
func TestDecodeBodyLimitBoundary(t *testing.T) {
	const limit = 4096
	for _, tc := range []struct {
		name string
		size int
		over bool
	}{
		{"恰好等于上限", limit, false},
		{"上限减一", limit - 1, false},
		{"上限加一", limit + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plain := bytes.Repeat([]byte("y"), tc.size)
			out, was, err := DecodeBodyLimit(gzipOf(t, plain), "gzip", limit)
			if tc.over {
				if !errors.Is(err, ErrBodyTooLarge) {
					t.Fatalf("err = %v,期望 ErrBodyTooLarge", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("未超限却报错: %v", err)
			}
			if !was || !bytes.Equal(out, plain) {
				t.Fatalf("解码结果不符:was=%v len=%d 期望 len=%d", was, len(out), len(plain))
			}
		})
	}
}

// limit <= 0 表示不设限:捕获侧的既有调用全走这条路,行为必须与 DecodeBody 逐字一致。
func TestDecodeBodyLimitUnlimited(t *testing.T) {
	plain := bytes.Repeat([]byte("z"), 1<<20)
	out, was, err := DecodeBodyLimit(gzipOf(t, plain), "gzip", 0)
	if err != nil || !was || !bytes.Equal(out, plain) {
		t.Fatalf("不设限时应原样解出:err=%v was=%v len=%d", err, was, len(out))
	}
}
