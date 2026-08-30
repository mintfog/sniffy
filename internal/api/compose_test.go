// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

// 本文件覆盖 compose.go 的请求、流停止和会话快照端点；出站 WebSocket 见 composews_test.go。

// TestComposeSuccessEnvelopes 成功响应提供 flowId/stopped 数据，供前端跳转会话详情和更新流状态。
func TestComposeSuccessEnvelopes(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)

	t.Run("发起请求回 flowId", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/compose", `{"method":"POST","url":"https://example.com/","body":"hi"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		e := decodeEnvelope(t, rec)
		if !e.Success {
			t.Error("success 应为 true")
		}
		var data struct {
			FlowID string `json:"flowId"`
		}
		e.into(t, &data)
		if data.FlowID != testComposer(t, s).newFlowID {
			t.Errorf("data.flowId = %q,期望 %q", data.FlowID, testComposer(t, s).newFlowID)
		}
		// spec 按原值传给 app 层，保证重发请求与用户输入一致。
		assertCalls(t, testComposer(t, s).calls,
			call{Method: "SendRequest", Args: []any{"POST", "https://example.com/", "hi"}})
	})

	t.Run("停止流回 stopped", func(t *testing.T) {
		rec := do(t, mux, http.MethodPost, "/api/compose/abc/stop", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		var data struct {
			Stopped bool `json:"stopped"`
		}
		decodeEnvelope(t, rec).into(t, &data)
		if !data.Stopped {
			t.Error("data.stopped 应为 true")
		}
		if got := lastCall(t, testComposer(t, s).calls, "StopStream"); got.Args[0] != "abc" {
			t.Errorf("StopStream 收到的 id = %v,期望 \"abc\"", got.Args[0])
		}
	})
}

// TestComposePathExtraSegmentsRejected 路径多余段统一返回 404，并保持流状态与构造器调用不变。
func TestComposePathExtraSegmentsRejected(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/api/compose/abc/stop/extra",
		"/api/compose/abc/pause",
		"/api/compose/abc",
		"/api/compose/",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			rec := do(t, mux, http.MethodPost, path, "")
			if rec.Code != http.StatusNotFound {
				t.Errorf("状态码 = %d,期望 404", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "not found" {
				t.Errorf("message = %q,期望 \"not found\"", e.Message)
			}
			assertNoCalls(t, testComposer(t, s).calls)
		})
	}
}

// TestComposeStreamStopBranches 未找到流返回 404 信封，前端据此区分已结束流和成功停止。
func TestComposeStreamStopBranches(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	testComposer(t, s).stopOK = false

	rec := do(t, mux, http.MethodPost, "/api/compose/abc/stop", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("状态码 = %d,期望 404", rec.Code)
	}
	if e := decodeEnvelope(t, rec); e.Success || e.Message != "stream not found" {
		t.Errorf("响应 = success:%v message:%q", e.Success, e.Message)
	}
}

// TestComposeUnavailableWithoutSender 未装配请求发送器时请求和停止端点统一返回 503 信封。
func TestComposeUnavailableWithoutSender(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t, withoutSender())
	for _, path := range []string{"/api/compose", "/api/compose/abc/stop"} {
		t.Run(path, func(t *testing.T) {
			rec := do(t, mux, http.MethodPost, path, "{}")
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("状态码 = %d,期望 503", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "request sender unavailable" {
				t.Errorf("message = %q", e.Message)
			}
		})
	}
}

// TestComposeSenderErrorIsPassedThrough 发送器的输入错误返回 400，并透传具体错误文案供构造器提示用户。
func TestComposeSenderErrorIsPassedThrough(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	testComposer(t, s).sendErr = errors.New("unsupported scheme: ftp")

	rec := do(t, mux, http.MethodPost, "/api/compose", `{"url":"ftp://x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d,期望 400(URL 无法解析、协议不受支持都是客户端输入问题)", rec.Code)
	}
	if e := decodeEnvelope(t, rec); e.Success || e.Message != "unsupported scheme: ftp" {
		t.Errorf("响应 = success:%v message:%q,期望原文透传", e.Success, e.Message)
	}
}

// TestComposeRoutePrecedence 验证 ServeMux 的最长前缀匹配，让 WebSocket 路径优先于流端点子树。
func TestComposeRoutePrecedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path       string
		wantMethod string
	}{
		{"/api/compose/ws", "OpenWebSocket"},
		{"/api/compose/ws/abc/send", "SendWSMessage"},
		{"/api/compose/ws/abc/close", "CloseWebSocket"},
		{"/api/compose/abc/stop", "StopStream"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			rec := do(t, mux, http.MethodPost, c.path, `{"type":"text","data":"hi"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
			}
			calls := testComposer(t, s).calls
			if len(calls) != 1 || calls[0].Method != c.wantMethod {
				t.Errorf("路由到了 %v,期望恰好一次 %s", calls, c.wantMethod)
			}
		})
	}
}

// TestSessionComposeSeed 会话快照完整保留请求行、头部和文本体，作为编辑重发的数据源。
func TestSessionComposeSeed(t *testing.T) {
	t.Parallel()
	const body = "user=alice&token=abc"
	s, mux := newTestServer(t)
	s.svc.RecordFlowCompleted(newFlowFixture("Flow-A",
		withRequest(http.MethodPost, "https://api.example.com/v1/login", "api.example.com"),
		withRequestHeader("Authorization", "Bearer upstream-token"),
		withRequestHeader("X-Trace", "t-1"),
		withRequestBody("application/x-www-form-urlencoded", []byte(body)),
	))

	rec := do(t, mux, http.MethodGet, "/api/sessions/Flow-A/compose", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var seed service.ComposeSeedDTO
	decodeEnvelope(t, rec).into(t, &seed)

	if seed.FlowID != "Flow-A" || seed.Method != http.MethodPost || seed.URL != "https://api.example.com/v1/login" {
		t.Errorf("请求行 = %q %s %s", seed.FlowID, seed.Method, seed.URL)
	}
	var auth string
	for _, kv := range seed.Headers {
		if strings.EqualFold(kv[0], "Authorization") {
			auth = kv[1]
		}
	}
	if auth != "Bearer upstream-token" {
		t.Errorf("Authorization 未逐字带上,got %q,全部头 %v", auth, seed.Headers)
	}
	if seed.Body != body || seed.BodySize != len(body) {
		t.Errorf("body/bodySize = %q/%d,期望 %q/%d", seed.Body, seed.BodySize, body, len(body))
	}
	if seed.BodyBinary || seed.BodyTooLarge {
		t.Errorf("文本体不应被标成二进制或超限: binary=%v tooLarge=%v", seed.BodyBinary, seed.BodyTooLarge)
	}

	t.Run("空 id 回 400", func(t *testing.T) {
		// 直调处理器可覆盖 mux cleanPath 之前的空 id 形态。
		rec := do(t, http.HandlerFunc(s.handleSession), http.MethodGet, "/api/sessions//compose", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "invalid session id" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("未知 id 回 404", func(t *testing.T) {
		rec := do(t, mux, http.MethodGet, "/api/sessions/nope/compose", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
	})
}
