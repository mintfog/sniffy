// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"net/http"
	"testing"
)

// TestApplyRequestToHTTP_PreservesTETrailers 锁定 gRPC-over-h2 修复:
// 含 `TE: trailers` 的请求转发后必须保留该头,否则严格的 gRPC 源站会拒绝。
func TestApplyRequestToHTTP_PreservesTETrailers(t *testing.T) {
	f := New(ProtoHTTPS)
	f.Request = &Request{
		Method: http.MethodPost,
		URL:    "https://grpc.example/helloworld.Greeter/SayHello",
		Host:   "grpc.example",
		Header: map[string][]string{
			"Te":           {"trailers"},
			"Content-Type": {"application/grpc"},
		},
		Body: []byte("payload"),
	}
	req, _ := http.NewRequest(http.MethodPost, "https://grpc.example/x", nil)
	req = ApplyRequestToHTTP(f, req)
	if got := req.Header.Get("TE"); got != "trailers" {
		t.Fatalf("TE 期望保留为 trailers,实得 %q", got)
	}
}

// TestApplyRequestToHTTP_StripsNonTrailerTE 确认非 trailers 的 TE 仍按逐跳头剔除。
func TestApplyRequestToHTTP_StripsNonTrailerTE(t *testing.T) {
	f := New(ProtoHTTPS)
	f.Request = &Request{
		Method: http.MethodGet,
		URL:    "https://example/",
		Host:   "example",
		Header: map[string][]string{"Te": {"gzip"}},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example/", nil)
	req = ApplyRequestToHTTP(f, req)
	if got := req.Header.Get("TE"); got != "" {
		t.Fatalf("非 trailers 的 TE 应被剔除,实得 %q", got)
	}
}
