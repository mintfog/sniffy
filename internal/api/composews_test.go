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

// recordingWSComposer 只记调用次数,用来断言超限请求根本没走到 app 层。
type recordingWSComposer struct {
	opens int
	sends int
	data  string
}

func (c *recordingWSComposer) OpenWebSocket(flow.RequestSpec) (string, error) {
	c.opens++
	return "ws-1", nil
}

func (c *recordingWSComposer) SendWSMessage(_, _, data string) error {
	c.sends++
	c.data = data
	return nil
}

func (c *recordingWSComposer) CloseWebSocket(string) error { return nil }

func wsRequest(path, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// 握手参数与 /api/compose 同源(都是一份 RequestSpec),上限也该同源:
// 没有上限,一个无限大的请求体在解码阶段就能把内存吃光。
func TestHandleComposeWSOpenRejectsOversizedBody(t *testing.T) {
	c := &recordingWSComposer{}
	s := &Server{wsComposer: c}

	huge := `{"url":"wss://example.com/ws","body":"` +
		strings.Repeat("x", int(maxComposeRequestBytes)+1) + `"}`
	w := httptest.NewRecorder()
	s.handleComposeWSOpen(w, wsRequest("/api/compose/ws", huge))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d,期望 %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if c.opens != 0 {
		t.Fatalf("超限的请求不该走到拨号,实际调用 %d 次", c.opens)
	}
}

// 出站帧同理:载荷还要经 base64 解码、会话副本与 DTO 广播被放大好几倍。
func TestHandleComposeWSSendRejectsOversizedBody(t *testing.T) {
	c := &recordingWSComposer{}
	s := &Server{wsComposer: c}

	huge := `{"type":"text","data":"` + strings.Repeat("x", int(maxComposeWSSendBytes)+1) + `"}`
	w := httptest.NewRecorder()
	s.handleComposeWSConn(w, wsRequest("/api/compose/ws/ws-1/send", huge))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d,期望 %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if c.sends != 0 {
		t.Fatalf("超限的帧不该走到发送,实际调用 %d 次", c.sends)
	}
}

// 上限之内的帧照常放行,data 逐字送到 app 层 —— 加限流不能顺手改了正常路径。
func TestHandleComposeWSSendAcceptsUnderLimit(t *testing.T) {
	c := &recordingWSComposer{}
	s := &Server{wsComposer: c}

	payload := strings.Repeat("y", 1024)
	w := httptest.NewRecorder()
	s.handleComposeWSConn(w, wsRequest("/api/compose/ws/ws-1/send", `{"type":"text","data":"`+payload+`"}`))

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,期望 200;响应体 = %s", w.Code, w.Body.String())
	}
	if c.data != payload {
		t.Fatal("data 未逐字送到 app 层")
	}
}
