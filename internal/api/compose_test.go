// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

// recordingSender 只记下最后一次收到的 spec,用来断言超限请求根本没走到发送。
type recordingSender struct {
	got   *flow.RequestSpec
	calls int
}

func (s *recordingSender) SendRequest(spec flow.RequestSpec) (string, error) {
	s.calls++
	s.got = &spec
	return "Flow-1", nil
}

func (s *recordingSender) StopStream(string) bool { return false }

func composeRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/compose", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// RequestSpec.Body 会被整体读进内存并作为 Flow.Body 长期留在会话存储里,
// 这条路径完全绕过响应侧的 passthrough / bodycache,没有别的兜底。
func TestHandleComposeRejectsOversizedBody(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{sender: sender}

	huge := `{"method":"POST","url":"https://example.com/","body":"` +
		strings.Repeat("x", int(maxComposeRequestBytes)+1) + `"}`
	w := httptest.NewRecorder()
	s.handleCompose(w, composeRequest(huge))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d,期望 %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if sender.calls != 0 {
		t.Fatalf("超限的请求不该走到发送,实际调用 %d 次", sender.calls)
	}
}

// 上限之内的请求照常放行,且 body 逐字送到 sender —— 加限流不能顺手改了正常路径。
func TestHandleComposeAcceptsBodyUnderLimit(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{sender: sender}

	payload := strings.Repeat("y", 1024)
	w := httptest.NewRecorder()
	s.handleCompose(w, composeRequest(`{"method":"POST","url":"https://example.com/","body":"`+payload+`"}`))

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,期望 200;响应体 = %s", w.Code, w.Body.String())
	}
	if sender.got == nil || sender.got.Body != payload {
		t.Fatal("body 未逐字送到 sender")
	}
}

// 畸形 JSON 仍归 400,不能被限流分支吃成 413。
func TestHandleComposeMalformedJSONStays400(t *testing.T) {
	sender := &recordingSender{}
	s := &Server{sender: sender}

	w := httptest.NewRecorder()
	s.handleCompose(w, composeRequest(`{"method":`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d,期望 400", w.Code)
	}
	if sender.calls != 0 {
		t.Fatalf("畸形输入不该走到发送,实际调用 %d 次", sender.calls)
	}
}
