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

// 本文件覆盖 composews.go 的出站 WebSocket 创建、发送和关闭端点。

// TestComposeWSPassesFlowIDAndType flow ID、消息类型和数据按原值传给构造器，二进制帧保持原始字节语义。
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

// TestComposeWSSuccessEnvelopes 成功响应包含前端驱动状态所需的 data 信封。
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

// TestComposeWSPathRejections 多余路径段和空 ID 按端点契约返回错误，构造器不产生调用。
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
			// 直调处理器覆盖 mux cleanPath 之前的空段形态。
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

// TestComposeWSErrorsArePassedThrough 出站构造器的错误文案原样返回 400，便于前端提示具体原因。
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

// TestComposeWSRejectsGET WebSocket 构造、发送、关闭和流停止均只接受 POST；GET 统一返回 405。
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

// TestComposeWSUnavailableWithoutComposer 未装配出站 WebSocket 构造器时相关端点统一返回 503 信封。
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
