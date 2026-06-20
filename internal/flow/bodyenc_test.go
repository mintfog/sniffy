// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"
)

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func buildGzipReqFlow(t *testing.T, encoded []byte) (*Flow, *http.Request) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://h.example/u", bytes.NewReader(encoded))
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/json")
	f := BuildRequestFlow(req, ProtoHTTP)
	return f, req
}

// TestRequestBodyFaithfulWhenUnmodified 校验:客户端发的 gzip 请求体在插件未改动时,
// 出站按原始压缩字节回放、保留 Content-Encoding(不被重编码成 identity)。
func TestRequestBodyFaithfulWhenUnmodified(t *testing.T) {
	encoded := gzipBytes(t, "hello faithful body")
	f, _ := buildGzipReqFlow(t, encoded)

	// Flow.Body 应是解码后的 identity 视图(供插件/UI)。
	if string(f.Request.Body) != "hello faithful body" {
		t.Fatalf("Flow.Body 应为解码后的 identity, 实得 %q", f.Request.Body)
	}

	out, _ := http.NewRequest(f.Request.Method, f.Request.URL, nil)
	out = ApplyRequestToHTTP(f, out)
	got, _ := io.ReadAll(out.Body)
	if !bytes.Equal(got, encoded) {
		t.Fatalf("未改动应回放原始 gzip 字节\n got=%x\nwant=%x", got, encoded)
	}
	if ce := out.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("应保留 Content-Encoding: gzip, 实得 %q", ce)
	}
	if out.ContentLength != int64(len(encoded)) {
		t.Fatalf("Content-Length 应为压缩字节长 %d, 实得 %d", len(encoded), out.ContentLength)
	}
}

// TestRequestBodyIdentityWhenModified 校验:插件改了请求体后,出站以 identity 重建、
// 删除 Content-Encoding、按新长度计 Content-Length。
func TestRequestBodyIdentityWhenModified(t *testing.T) {
	encoded := gzipBytes(t, "original")
	f, _ := buildGzipReqFlow(t, encoded)

	f.Request.Body = []byte("modified by plugin") // 模拟插件改体

	out, _ := http.NewRequest(f.Request.Method, f.Request.URL, nil)
	out = ApplyRequestToHTTP(f, out)
	got, _ := io.ReadAll(out.Body)
	if string(got) != "modified by plugin" {
		t.Fatalf("改动后应发送新 identity body, 实得 %q", got)
	}
	if ce := out.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("改动后应删除 Content-Encoding, 实得 %q", ce)
	}
	if out.ContentLength != int64(len("modified by plugin")) {
		t.Fatalf("Content-Length 应按新 body 计, 实得 %d", out.ContentLength)
	}
}
