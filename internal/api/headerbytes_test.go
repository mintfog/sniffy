// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/base64"
	"net/http"
	"slices"
	"testing"
)

// b64Slot 使用线上契约的标准 base64 编码。
func b64Slot(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// 放行入口递归校验未知字段，headersB64 位于对应的编辑对象中。
func TestBreakpointResumeAcceptsHeadersB64(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	id, _, wait := pausedFlow(t, bp)

	body := `{"request":{"headers":[["Host","x.com"],["X-Note","shown"]],` +
		`"headersB64":["","` + b64Slot("raw-\xe9") + `"]}}`
	rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("带旁路的放行 = %d，期望 200；响应体 = %s", rec.Code, rec.Body.String())
	}
	wait()
}

// 严格解码接受 headersB64 字段名，拼写错误返回 400。
func TestBreakpointResumeRejectsMisspelledSidecarField(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	id, _, wait := pausedFlow(t, bp)

	body := `{"request":{"headers":[["Host","x.com"]],"headersRawB64":[""]}}`
	rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知字段 = %d，期望 400", rec.Code)
	}
	if len(bp.List()) != 1 {
		t.Fatal("被拒绝的放行不应把 flow 从断点上放走")
	}

	// 错误请求保留暂停状态，后续请求可继续处理。
	if rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
		t.Fatalf("改回来后放行 = %d，期望 200", rec.Code)
	}
	wait()
}

// 畸形旁路返回 400，flow 保持暂停状态。
func TestBreakpointResumeMalformedSidecarIsBadRequest(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	bp := s.pipe.Breakpoints()
	id, _, wait := pausedFlow(t, bp)

	// headersB64 数量必须与 headers 数量对应。
	body := `{"request":{"headers":[["Host","x.com"]],"headersB64":["","` + b64Slot("a") + `"]}}`
	rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("项数对不上的旁路 = %d，期望 400；响应体 = %s", rec.Code, rec.Body.String())
	}
	if len(bp.List()) != 1 {
		t.Fatal("被拒绝的放行不应把 flow 从断点上放走")
	}

	// 错误请求保留暂停状态，后续请求可继续处理。
	if rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
		t.Fatalf("改回来后放行 = %d，期望 200", rec.Code)
	}
	wait()
}

// 构造器入口按头部顺序传递值字节旁路。
func TestComposeCarriesHeadersB64(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	composer := testComposer(t, s)

	slot := b64Slot("raw-\xe9")
	body := `{"method":"GET","url":"https://example.com/","headers":[["Host","example.com"],["X-Note","shown"]],` +
		`"headersB64":["","` + slot + `"]}`
	rec := do(t, mux, http.MethodPost, "/api/compose", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200；响应体 = %s", rec.Code, rec.Body.String())
	}
	if len(composer.specs) != 1 {
		t.Fatal("请求未走到发送")
	}
	if got := composer.specs[0].HeadersB64; !slices.Equal(got, []string{"", slot}) {
		t.Fatalf("旁路 = %q，期望 [%q %q]", got, "", slot)
	}
}
