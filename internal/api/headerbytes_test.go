// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// b64Slot 使用线上契约的标准 base64 编码。
func b64Slot(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// 放行入口递归校验未知字段，headersB64 必须位于对应的编辑对象中。
func TestBreakpointResumeAcceptsHeadersB64(t *testing.T) {
	s, mux := wiredServer()
	id, wait := pauseOne(t, s.pipe.Breakpoints())

	body := `{"request":{"headers":[["Host","x.com"],["X-Note","shown"]],` +
		`"headersB64":["","` + b64Slot("raw-\xe9") + `"]}}`
	rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("带旁路的放行 = %d, want 200;响应体 = %s", rec.Code, rec.Body.String())
	}
	wait()
}

// 严格解码仅接受 headersB64 字段名，拼写错误返回 400。
func TestBreakpointResumeRejectsMisspelledSidecarField(t *testing.T) {
	s, mux := wiredServer()
	id, wait := pauseOne(t, s.pipe.Breakpoints())

	body := `{"request":{"headers":[["Host","x.com"]],"headersRawB64":[""]}}`
	rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知字段 = %d, want 400", rec.Code)
	}
	if len(s.pipe.Breakpoints().List()) != 1 {
		t.Fatal("被拒绝的放行不应把 flow 从断点上放走")
	}

	// 错误请求保留暂停状态，后续请求可继续处理。
	if rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
		t.Fatalf("改回来后放行 = %d", rec.Code)
	}
	wait()
}

// 畸形旁路返回 400，flow 保持暂停状态。
func TestBreakpointResumeMalformedSidecarIsBadRequest(t *testing.T) {
	s, mux := wiredServer()
	id, wait := pauseOne(t, s.pipe.Breakpoints())

	body := `{"request":{"headers":[["Host","x.com"]],"headersB64":["","` + b64Slot("a") + `"]}}`
	rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("长度对不上的旁路 = %d, want 400;响应体 = %s", rec.Code, rec.Body.String())
	}
	if len(s.pipe.Breakpoints().List()) != 1 {
		t.Fatal("被拒绝的放行不应把 flow 从断点上放走")
	}

	// 错误请求保留暂停状态，后续请求可继续处理。
	if rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
		t.Fatalf("改回来后放行 = %d", rec.Code)
	}
	wait()
}

// 构造器入口按头部顺序传递值字节旁路。
func TestHandleComposeCarriesHeadersB64(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{sender: sender}

	slot := b64Slot("raw-\xe9")
	body := `{"method":"GET","url":"https://example.com/","headers":[["Host","example.com"],["X-Note","shown"]],` +
		`"headersB64":["","` + slot + `"]}`
	w := httptest.NewRecorder()
	s.handleCompose(w, composeRequest(body))

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,期望 200;响应体 = %s", w.Code, w.Body.String())
	}
	if sender.got == nil {
		t.Fatal("请求未走到发送")
	}
	want := []string{"", slot}
	if len(sender.got.HeadersB64) != len(want) {
		t.Fatalf("旁路 = %q, want %q", sender.got.HeadersB64, want)
	}
	for i := range want {
		if sender.got.HeadersB64[i] != want[i] {
			t.Fatalf("旁路第 %d 项 = %q, want %q", i, sender.got.HeadersB64[i], want[i])
		}
	}
}
