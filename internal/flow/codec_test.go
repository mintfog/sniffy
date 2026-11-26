// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"testing"
	"testing/iotest"

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

// 显式不限量调用的结果须与 DecodeBody 一致。
func TestDecodeBodyLimitUnlimited(t *testing.T) {
	plain := bytes.Repeat([]byte("z"), 1<<20)
	out, was, err := DecodeBodyLimit(gzipOf(t, plain), "gzip", 0)
	if err != nil || !was || !bytes.Equal(out, plain) {
		t.Fatalf("不设限时应原样解出:err=%v was=%v len=%d", err, was, len(out))
	}
}

func TestCaptureResponseBodyLimit(t *testing.T) {
	const limit = 4096
	for _, encoding := range []string{"", "gzip"} {
		for _, size := range []int{limit - 1, limit, limit + 1} {
			plain := bytes.Repeat([]byte("x"), size)
			wire := plain
			if encoding == "gzip" {
				wire = gzipOf(t, plain)
			}
			resp := &http.Response{Body: io.NopCloser(bytes.NewReader(wire)), ContentLength: -1, Header: http.Header{"Content-Encoding": {encoding}}}
			f := New(ProtoHTTP)
			err := CaptureResponseToFlowLimit(f, resp, limit)
			if errors.Is(err, ErrBodyTooLarge) != (size > limit) {
				t.Fatalf("编码=%q，正文大小=%d，错误=%v", encoding, size, err)
			}
			if !bytes.Equal(f.Response.Body, plain[:min(size, limit)]) {
				t.Fatalf("编码=%q，正文大小=%d，捕获正文不符", encoding, size)
			}
		}
	}
}

// incompressible 返回压缩后几乎不变小的正文，用来让传输字节本身就超过上限。
func incompressible(size int) []byte {
	out := make([]byte, size)
	r := rand.NewChaCha8([32]byte{1})
	_, _ = r.Read(out)
	return out
}

func encodedWith(t *testing.T, plain []byte, encoding string) []byte {
	t.Helper()
	switch encoding {
	case "gzip-store":
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.NoCompression)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = zw.Write(plain)
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	case "zstd":
		zw, err := zstd.NewWriter(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer zw.Close()
		return zw.EncodeAll(plain, nil)
	case "br":
		var buf bytes.Buffer
		bw := brotli.NewWriter(&buf)
		_, _ = bw.Write(plain)
		if err := bw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	wire, ok := EncodeBody(plain, encoding)
	if !ok {
		t.Fatalf("无法按 %s 压缩", encoding)
	}
	return wire
}

// TestCaptureResponseLimitDecodesOversizedWire：压缩后的传输字节已超上限时，正文是解压后的前 limit
// 字节，并记下响应编码。
func TestCaptureResponseLimitDecodesOversizedWire(t *testing.T) {
	const limit = 4096
	plain := incompressible(3 * limit)
	for _, encoding := range []string{"gzip", "deflate", "zstd", "br"} {
		wire := encodedWith(t, plain, encoding)
		if len(wire) <= limit {
			t.Fatalf("%s：传输字节 %d 未超过上限，测不到这条路径", encoding, len(wire))
		}
		resp := &http.Response{Body: io.NopCloser(bytes.NewReader(wire)), Header: http.Header{"Content-Encoding": {encoding}}}
		f := New(ProtoHTTP)
		err := CaptureResponseToFlowLimit(f, resp, limit)
		if !errors.Is(err, ErrBodyTooLarge) {
			t.Fatalf("%s：错误=%v，期望超限", encoding, err)
		}
		if !bytes.Equal(f.Response.Body, plain[:limit]) {
			t.Fatalf("%s：正文不是解压后的前 %d 字节（开头 % x）", encoding, limit, f.Response.Body[:4])
		}
		if f.Metadata[metaRespEncoding] != encoding {
			t.Fatalf("%s：未记录响应编码", encoding)
		}
	}
}

// TestCaptureResponseLimitWireOverDecodedWithin：传输字节超上限而解压后在上限内（不压缩的 gzip
// 带着格式开销）时拿到完整正文；此时已读的线上字节不完整，不能登记为原始编码体。
func TestCaptureResponseLimitWireOverDecodedWithin(t *testing.T) {
	const limit = 4096
	plain := incompressible(limit - 8)
	wire := encodedWith(t, plain, "gzip-store")
	if len(wire) <= limit {
		t.Fatalf("传输字节 %d 未超过上限", len(wire))
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	capture := &ResponseCapture{Headers: [][2]string{{"Content-Encoding", "gzip"}}}
	req = req.WithContext(WithResponseCapture(req.Context(), capture))
	resp := &http.Response{Request: req, Body: io.NopCloser(bytes.NewReader(wire)), Header: http.Header{"Content-Encoding": {"gzip"}}}
	f := New(ProtoHTTP)
	if err := CaptureResponseToFlowLimit(f, resp, limit); err != nil {
		t.Fatalf("错误=%v", err)
	}
	if !bytes.Equal(f.Response.Body, plain) {
		t.Fatal("正文不完整")
	}
	if f.Response.OriginalEncodedBody(f.Response.Body) != nil {
		t.Fatal("线上字节只读了一截，不应登记为原始编码体")
	}
}

// TestCaptureResponseLimitOversizedWireReadFailure：超限后继续读连接时中断，保留已解出的部分并
// 交回读取错误。
func TestCaptureResponseLimitOversizedWireReadFailure(t *testing.T) {
	const limit = 4096
	readErr := errors.New("上游中断")
	plain := incompressible(3 * limit)
	wire := encodedWith(t, plain, "gzip")
	cut := limit + 512
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": {"gzip"}},
		Body:   io.NopCloser(io.MultiReader(bytes.NewReader(wire[:cut]), iotest.ErrReader(readErr))),
	}
	f := New(ProtoHTTP)
	if err := CaptureResponseToFlowLimit(f, resp, limit); !errors.Is(err, readErr) {
		t.Fatalf("错误=%v，期望=%v", err, readErr)
	}
	body := f.Response.Body
	if len(body) == 0 || len(body) > limit || !bytes.Equal(body, plain[:len(body)]) {
		t.Fatalf("正文应为解压后的一段前缀，实际 %d 字节", len(body))
	}
}

func TestCaptureResponsePassthroughBoundaries(t *testing.T) {
	const limit = 4096
	for _, encoding := range []string{"", "gzip"} {
		for _, size := range []int{limit - 1, limit, limit + 1} {
			plain := bytes.Repeat([]byte("x"), size)
			wire := plain
			if encoding == "gzip" {
				wire = gzipOf(t, plain)
			}
			resp := &http.Response{Body: io.NopCloser(bytes.NewReader(wire)), Header: http.Header{"Content-Encoding": {encoding}}}
			f := New(ProtoHTTP)
			err := captureResponseToFlow(f, resp, limit, true)
			if errors.Is(err, ErrBodyTooLarge) != (size > limit) {
				t.Fatalf("编码=%q，正文大小=%d，错误=%v", encoding, size, err)
			}
			replay, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if size > limit {
				if len(f.Response.Body) != 0 || !bytes.Equal(replay, wire) {
					t.Fatalf("编码=%q，超限后未恢复完整线上正文", encoding)
				}
			} else if !bytes.Equal(f.Response.Body, plain) || !bytes.Equal(replay, plain) {
				t.Fatalf("编码=%q，正文大小=%d，捕获正文不符", encoding, size)
			}
		}
	}
}

// TestCaptureResponsePassthroughReleasesPrefix：转透传后，探测上限时缓冲的前缀发完即释放，其后的
// 正文接着从上游读出。
func TestCaptureResponsePassthroughReleasesPrefix(t *testing.T) {
	const limit = 4096
	wire := incompressible(3 * limit)
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader(wire)), Header: http.Header{}}
	if err := captureResponseToFlow(New(ProtoHTTP), resp, limit, true); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("错误=%v，期望超限", err)
	}
	replay := resp.Body.(*responseReplayBody)
	head := make([]byte, limit+1)
	if _, err := io.ReadFull(replay, head); err != nil {
		t.Fatal(err)
	}
	if replay.prefix != nil {
		t.Fatal("前缀发完后仍被引用")
	}
	rest, err := io.ReadAll(replay)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(head, rest...), wire) {
		t.Fatal("回放的线上字节不完整")
	}
}

func TestCaptureResponseReadFailureDoesNotRegisterOriginalBody(t *testing.T) {
	readErr := errors.New("上游中断")
	compressed := gzipOf(t, []byte("complete gzip data"))
	req, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	capture := &ResponseCapture{Headers: [][2]string{{"Content-Encoding", "gzip"}}}
	req = req.WithContext(WithResponseCapture(req.Context(), capture))
	resp := &http.Response{
		Request: req,
		Header:  http.Header{"Content-Encoding": {"gzip"}},
		Body:    io.NopCloser(io.MultiReader(bytes.NewReader(compressed), iotest.ErrReader(readErr))),
	}
	f := New(ProtoHTTP)
	if err := CaptureResponseToFlow(f, resp); !errors.Is(err, readErr) {
		t.Fatalf("读取错误=%v，期望=%v", err, readErr)
	}
	if f.Response.OriginalEncodedBody(f.Response.Body) != nil {
		t.Fatal("读取失败后不应登记完整编码正文")
	}
}

func TestReadAllLimitPreservesReadError(t *testing.T) {
	readErr := errors.New("读取中断")
	r := iotest.DataErrReader(io.MultiReader(bytes.NewReader([]byte("abcde")), iotest.ErrReader(readErr)))
	got, err := readAllLimit(r, 4)
	if !errors.Is(err, readErr) || string(got) != "abcd" {
		t.Fatalf("正文=%q，错误=%v；期望正文=abcd，错误=%v", got, err, readErr)
	}
}

func TestDecodeBodyLimitBoundsZstdWindow(t *testing.T) {
	// 空帧声明 32 MiB 窗口；正文大小限制不能阻止解码器提前分配窗口。
	body := []byte{0x28, 0xb5, 0x2f, 0xfd, 0, 15 << 3, 1, 0, 0}
	for _, limit := range []int64{4096, 32 << 20, 0} {
		_, _, err := DecodeBodyLimit(body, "zstd", limit)
		if errors.Is(err, ErrBodyTooLarge) != (limit == 4096) {
			t.Fatalf("解码上限=%d，窗口检查错误=%v", limit, err)
		}
		if limit != 4096 && err != nil {
			t.Fatalf("解码上限=%d，允许的窗口返回错误=%v", limit, err)
		}
	}
}

func BenchmarkCaptureResponseToFlowGzipBomb(b *testing.B) {
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	if _, err := w.Write(bytes.Repeat([]byte("x"), 32<<20)); err != nil {
		b.Fatal(err)
	}
	if err := w.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		resp := &http.Response{
			Header: http.Header{"Content-Encoding": {"gzip"}},
			Body:   io.NopCloser(bytes.NewReader(compressed.Bytes())),
		}
		_ = CaptureResponseToFlow(New(ProtoHTTP), resp)
		_ = resp.Body.Close()
	}
}
