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

// 本文件对应 compose.go 的三条路径:POST /api/compose、POST /api/compose/{id}/stop、
// GET /api/sessions/{id}/compose。出站 WebSocket 那套在 composews_test.go。

// TestComposeSuccessEnvelopes 前端拿 data.flowId 跳转新建的会话详情。字段名改成 id、
// 或忘了包 ok() 的 data 信封,只比状态码的测试全绿而界面上「发送」后停在空白页。
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
		// spec 逐字送到 app 层:body 被截断或字段错位时,用户重发出去的是另一条请求。
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

// TestComposePathExtraSegmentsRejected 安全基线要求路径多余段一律 404、不退化成对父资源动手。
// 现在靠 strings.Cut 只切第一段这一实现细节保住,一个看似无害的改法(Split(...)[1] 或先 TrimSuffix("/"))
// 就会让 /api/compose/abc/stop/typo 真的把用户的 SSE 流掐断。
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

// TestComposeStreamStopBranches 「未命中」被改成 200 时,前端会把一条早已结束的 SSE 标成「已停止」,
// 用户以为流被自己掐掉了,而服务端根本没找到它 —— 排查会朝完全错误的方向去。
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

// TestComposeUnavailableWithoutSender 两个 nil 判空块此前执行计数为 0。判空被误删后,
// 未装配 sender 的部署会 nil 指针 panic —— 用户拿到的是连接被重置而不是可读的 503。
func TestComposeUnavailableWithoutSender(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t, withoutSender(), withoutComposer())
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

// TestComposeSenderErrorIsPassedThrough 构造器面板唯一的错误提示就是这段 message:改成 500 或
// 笼统的 "send failed",用户填错 URL/协议后只会看到「服务器错误」,无从知道是自己少写了 scheme。
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

// TestComposeRoutePrecedence 钉住 ServeMux 的最长前缀匹配:/api/compose/ws 这一支必须由
// WebSocket handler 接管,不能被 /api/compose/ 的子树当成 id 为 "ws" 的流。
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

// TestSessionComposeSeed 这份快照是「编辑重发」的唯一数据源:seed 少带一个 header(比如 Authorization)
// 或 body 被截断,用户重发出去的是一条与原始请求不等价的请求,然后对着两条看起来一样的会话
// 排查为什么一条 200 一条 401。
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
		// 直调 handler:mux 的 cleanPath 到不了这条形态。
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
