// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"errors"
	"net/http"
	"testing"
)

// 本文件对应 composews.go:构造器发起并驾驭一条出站 WebSocket。

// TestComposeWSPassesFlowIDAndType handler 若把 action 当成 id 传下去、或把 body.Type 漏传成空串,
// 只看状态码的测试一条都不会红;用户看到的是帧被发到另一条连接(多标签构造器里向错误的
// WebSocket 注入数据),或二进制/ping 帧被当作 text 发出,对端收到 base64 原文而不是原始字节。
func TestComposeWSPassesFlowIDAndType(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)

	if rec := do(t, mux, http.MethodPost, "/api/compose/ws/ws-42/send", `{"type":"binary","data":"AQID"}`); rec.Code != http.StatusOK {
		t.Fatalf("send 状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, mux, http.MethodPost, "/api/compose/ws/ws-42/close", ""); rec.Code != http.StatusOK {
		t.Fatalf("close 状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	assertCalls(t, testComposer(t, s).calls,
		call{Method: "SendWSMessage", Args: []any{"ws-42", "binary", "AQID"}},
		call{Method: "CloseWebSocket", Args: []any{"ws-42"}},
	)
}

// TestComposeWSSuccessEnvelopes 前端按 data 字段驱动界面状态,字段改名或忘包信封时
// 「发送」「关闭」按钮永远停在 loading。
func TestComposeWSSuccessEnvelopes(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)

	t.Run("open 回 flowId", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/compose/ws", `{"url":"wss://example.com/ws"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		var data struct {
			FlowID string `json:"flowId"`
		}
		decodeEnvelope(t, rec).into(t, &data)
		if data.FlowID != testComposer(t, s).newWSID {
			t.Errorf("data.flowId = %q,期望 %q", data.FlowID, testComposer(t, s).newWSID)
		}
	})

	t.Run("send 回 sent", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/compose/ws/ws-1/send", `{"type":"text","data":"hi"}`)
		var data struct {
			Sent bool `json:"sent"`
		}
		decodeEnvelope(t, rec).into(t, &data)
		if rec.Code != http.StatusOK || !data.Sent {
			t.Errorf("状态码/sent = %d/%v", rec.Code, data.Sent)
		}
	})

	t.Run("close 回 closed", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/compose/ws/ws-1/close", "")
		var data struct {
			Closed bool `json:"closed"`
		}
		decodeEnvelope(t, rec).into(t, &data)
		if rec.Code != http.StatusOK || !data.Closed {
			t.Errorf("状态码/closed = %d/%v", rec.Code, data.Closed)
		}
	})
}

// TestComposeWSPathRejections 同属「多余段不得退化」基线。改成 Split(...)[1] 或先 TrimSuffix("/") 后,
// /api/compose/ws/abc/send/anything 会真的把帧发出去。
func TestComposeWSPathRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path    string
		want    int
		wantMsg string
	}{
		{"/api/compose/ws//send", http.StatusBadRequest, "invalid websocket id"},
		{"/api/compose/ws/", http.StatusBadRequest, "invalid websocket id"},
		{"/api/compose/ws/abc/send/extra", http.StatusNotFound, "not found"},
		{"/api/compose/ws/abc", http.StatusNotFound, "not found"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			t.Parallel()
			s, _ := newTestServer(t)
			// 直调 handler:mux 的 cleanPath 会把 // 折叠掉,那几条形态到不了处理器。
			rec := do(t, http.HandlerFunc(s.handleComposeWSConn), http.MethodPost, c.path, `{"type":"text","data":"hi"}`)
			if rec.Code != c.want {
				t.Errorf("状态码 = %d,期望 %d", rec.Code, c.want)
			}
			if e := decodeEnvelope(t, rec); e.Message != c.wantMsg {
				t.Errorf("message = %q,期望 %q", e.Message, c.wantMsg)
			}
			assertNoCalls(t, testComposer(t, s).calls)
		})
	}
}

// TestComposeWSErrorsArePassedThrough composews.go 的注释写着「真正超限的帧仍由 app 层以
// 『单帧载荷超过上限』拒绝,文案比一个笼统的 413 更有用」。文案在 transport 被吞掉时,
// 用户在构造器里发一个 9 MiB 的帧只会看到帧「发出去了」,而对端什么也没收到。
func TestComposeWSErrorsArePassedThrough(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		setErr func(*recordingComposer, error)
		path   string
		body   string
	}{
		{"send", func(c *recordingComposer, err error) { c.sendWSErr = err }, "/api/compose/ws/x/send", `{"type":"text","data":"hi"}`},
		{"close", func(c *recordingComposer, err error) { c.closeErr = err }, "/api/compose/ws/x/close", ""},
		{"open", func(c *recordingComposer, err error) { c.openErr = err }, "/api/compose/ws", `{"url":"wss://x/"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := errors.New("单帧载荷超过上限: " + c.name)
			s, mux := newTestServer(t)
			c.setErr(testComposer(t, s), want)

			rec := do(t, mux, http.MethodPost, c.path, c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d,期望 400", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Success || e.Message != want.Error() {
				t.Errorf("响应 = success:%v message:%q,期望原文透传 %q", e.Success, e.Message, want)
			}
		})
	}
}

// TestComposeWSRejectsGET 无 token 的兜底路径下同源检查挡不住浏览器发起的顶层导航 / <img src>
// 一类 GET(Sec-Fetch-Site 可能是 none、Host 是回环、Origin 缺省),方法检查是唯一的关口。
func TestComposeWSRejectsGET(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/api/compose/ws",
		"/api/compose/ws/x/send",
		"/api/compose/ws/x/close",
		"/api/compose/x/stop",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			rec := do(t, mux, http.MethodGet, path, "")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("状态码 = %d,期望 405", rec.Code)
			}
			assertNoCalls(t, testComposer(t, s).calls)
		})
	}
}

// TestComposeWSUnavailableWithoutComposer 未装配出站 WebSocket 时统一回 503,而不是 nil 指针 panic。
func TestComposeWSUnavailableWithoutComposer(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t, withoutComposer())
	for _, path := range []string{"/api/compose/ws", "/api/compose/ws/x/send", "/api/compose/ws/x/close"} {
		t.Run(path, func(t *testing.T) {
			rec := do(t, mux, http.MethodPost, path, "{}")
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("状态码 = %d,期望 503", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "websocket composer unavailable" {
				t.Errorf("message = %q", e.Message)
			}
		})
	}
}
