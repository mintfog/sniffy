// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/iotest"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

type observedResponseBody struct {
	io.Reader
	closed bool
}

func (b *observedResponseBody) Close() error { b.closed = true; return nil }

func encodeResponseBody(t *testing.T, plain []byte, encoding string) []byte {
	t.Helper()
	if encoding == "" {
		return plain
	}
	var buf bytes.Buffer
	var w io.WriteCloser
	switch encoding {
	case "gzip":
		w = gzip.NewWriter(&buf)
	case "deflate":
		w, _ = flate.NewWriter(&buf, flate.DefaultCompression)
	case "br":
		w = brotli.NewWriterLevel(&buf, 1)
	case "zstd":
		w, _ = zstd.NewWriter(&buf, zstd.WithEncoderConcurrency(1))
	default:
		t.Fatalf("未知编码 %q", encoding)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBufferedResponseLimitForwardsCompleteBody(t *testing.T) {
	plain := bytes.Repeat([]byte("abcdef0123456789"), (16<<20)/16+1)
	for _, encoding := range []string{"", "gzip", "deflate", "br", "zstd"} {
		wire := encodeResponseBody(t, plain, encoding)
		for _, knownLength := range []bool{false, true} {
			for _, protocol := range []string{"h1", "h2"} {
				t.Run(fmt.Sprintf("编码=%s/已知长度=%v/%s", encoding, knownLength, protocol), func(t *testing.T) {
					preserveHTTPGlobals(t)
					previousPassthrough, previousCache := passthroughEnabled.Load(), bodyCache.Load()
					t.Cleanup(func() { passthroughEnabled.Store(previousPassthrough); SetBodyCache(previousCache) })
					passthroughEnabled.Store(false)
					cache, err := bodycache.New(t.TempDir(), 0)
					if err != nil {
						t.Fatal(err)
					}
					SetBodyCache(cache)
					sink := &collectingSink{}
					flowSink, processResolver = sink, nil
					activePipeline = pipeline.New(nil, nil)
					hookCalls := 0
					activePipeline.Register(&flowDecisionHook{onResponse: func(f *flow.Flow) flow.Decision {
						hookCalls++
						if len(f.Response.Body) != 0 || f.Metadata["passthrough"] != true {
							t.Fatal("透传插件收到了不完整正文")
						}
						f.Response.Header["X-Plugin"] = []string{"applied"}
						f.Response.Header["Content-Encoding"] = []string{"incorrect"}
						f.Response.Header["content-encoding"] = []string{"incorrect"}
						return flow.ContinueDecision()
					}})
					body := &observedResponseBody{Reader: bytes.NewReader(wire)}
					length := int64(-1)
					if knownLength {
						length = int64(len(wire))
					}
					sharedHttpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: http.StatusOK, Status: "200 OK",
							Header:        http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {encoding}},
							ContentLength: length, Body: body, Request: req,
							Trailer: http.Header{"X-Checksum": {"complete"}},
						}, nil
					})}
					req, _ := http.NewRequest(http.MethodGet, "http://example.test/data", nil)
					conn := newTimedConn("")
					recorder := httptest.NewRecorder()
					var w bodyStreamer = newConnBodyStreamer(conn, 0)
					if protocol == "h2" {
						w = newH2BodyStreamer(recorder)
					}
					r := &branchResponder{body: w, bodyOK: true}
					if err := runFlowPipeline(silentServer{}, req, flow.ProtoHTTP, nil, nil, r); err != nil {
						t.Fatal(err)
					}
					if r.badGatewayCalls != 0 || r.reuseDisabled || hookCalls != 1 || !body.closed {
						t.Fatalf("转发状态不符：502=%d，禁用复用=%v，插件调用=%d，关闭正文=%v", r.badGatewayCalls, r.reuseDisabled, hookCalls, body.closed)
					}
					var received *http.Response
					if protocol == "h1" {
						received, err = http.ReadResponse(bufio.NewReader(bytes.NewReader(conn.bytes())), req)
						if err != nil {
							t.Fatal(err)
						}
					} else {
						received = recorder.Result()
					}
					defer received.Body.Close()
					got, err := io.ReadAll(received.Body)
					if err != nil || received.StatusCode != http.StatusOK || !bytes.Equal(got, wire) {
						t.Fatalf("响应未完整转发：状态=%d，正文=%d/%d，错误=%v", received.StatusCode, len(got), len(wire), err)
					}
					if received.Header.Get("Content-Encoding") != encoding || received.Header.Get("X-Plugin") != "applied" {
						t.Fatalf("响应头与线上正文不符：%v", received.Header)
					}
					if knownLength && received.ContentLength != length {
						t.Fatalf("长度=%d，期望=%d", received.ContentLength, length)
					}
					if !knownLength && received.Trailer.Get("X-Checksum") != "complete" {
						t.Fatalf("尾部丢失：%v", received.Trailer)
					}
					f := sink.last()
					if f == nil || f.State != flow.StateCompleted || len(f.Response.Body) != 0 || f.Response.BodyLen() != int64(len(wire)) {
						t.Fatalf("会话记录不符：%+v", f)
					}
					path, _ := f.Response.BodyFile()
					if encoding == "" {
						cached, err := os.ReadFile(path)
						if err != nil || !bytes.Equal(cached, plain) {
							t.Fatalf("缓存正文不完整：%v", err)
						}
					} else if path != "" {
						t.Fatal("编码字节不应作为 identity 正文缓存")
					}
				})
			}
		}
	}
}

func TestBufferedResponseLimitPreservesReadFailure(t *testing.T) {
	preserveHTTPGlobals(t)
	previousPassthrough := passthroughEnabled.Load()
	t.Cleanup(func() { passthroughEnabled.Store(previousPassthrough) })
	passthroughEnabled.Store(false)
	readErr := errors.New("上游读取中断")
	wire := bytes.Repeat([]byte("x"), (16<<20)+1)
	body := &observedResponseBody{Reader: iotest.DataErrReader(io.MultiReader(bytes.NewReader(wire), iotest.ErrReader(readErr)))}
	sharedHttpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: -1, Body: body, Request: req}, nil
	})}
	flowSink, processResolver, activePipeline = nil, nil, nil
	req, _ := http.NewRequest(http.MethodGet, "http://example.test/data", nil)
	conn := newTimedConn("")
	r := &branchResponder{body: newConnBodyStreamer(conn, 0), bodyOK: true}
	err := runFlowPipeline(silentServer{}, req, flow.ProtoHTTP, nil, nil, r)
	if !errors.Is(err, readErr) || !r.reuseDisabled || !body.closed || r.badGatewayCalls != 0 {
		t.Fatalf("读取错误未保留：%v，响应状态=%+v", err, r)
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(conn.bytes())), req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	if err == nil || !bytes.Equal(got, wire) {
		t.Fatalf("截断响应不应正常结束：字节=%d/%d，错误=%v", len(got), len(wire), err)
	}
}

func BenchmarkRunFlowPipelineSmallBody(b *testing.B) {
	previousClient, previousPipeline, previousSink, previousResolver := sharedHttpClient, activePipeline, flowSink, processResolver
	b.Cleanup(func() {
		sharedHttpClient, activePipeline, flowSink, processResolver = previousClient, previousPipeline, previousSink, previousResolver
	})
	activePipeline = pipeline.New(nil, nil)
	flowSink, processResolver = nil, nil
	body := bytes.Repeat([]byte("x"), 4096)
	sharedHttpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {"text/plain"}},
			ContentLength: int64(len(body)),
			Body:          io.NopCloser(bytes.NewReader(body)),
			Request:       req,
		}, nil
	})}
	b.ReportAllocs()
	b.SetBytes(int64(2 * len(body)))
	for b.Loop() {
		req, _ := http.NewRequest(http.MethodPost, "http://example.test/data", bytes.NewReader(body))
		r := &branchResponder{}
		if err := runFlowPipeline(silentServer{}, req, flow.ProtoHTTP, nil, nil, r); err != nil {
			b.Fatal(err)
		}
		if r.written == nil || r.written.State != flow.StateCompleted {
			b.Fatal("正常正文未完成转发")
		}
	}
}
