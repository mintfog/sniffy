// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// 本文件覆盖 sessions.go 的详情、删除、清空、列表信封、ID 解析及 WebSocket/流式详情端点；body 端点见 sessionbody_test.go。

// TestSessionDetailEnvelope 详情返回 apiResponse 信封，并包含会话 ID、请求行和响应。
func TestSessionDetailEnvelope(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	s.svc.RecordFlowCompleted(newFlowFixture("Flow-A",
		withRequest(http.MethodPost, "https://api.example.com/v1/ping", "api.example.com"),
		withResponse(http.StatusCreated, "application/json", []byte(`{"ok":true}`)),
	))

	t.Run("命中", func(t *testing.T) {
		rec := do(t, mux, http.MethodGet, "/api/sessions/Flow-A", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		e := decodeEnvelope(t, rec)
		if !e.Success {
			t.Error("success 应为 true")
		}
		var dto service.HTTPSessionDTO
		e.into(t, &dto)
		if dto.ID != "Flow-A" {
			t.Errorf("data.id = %q,期望 \"Flow-A\"", dto.ID)
		}
		if dto.Request.Method != http.MethodPost || dto.Request.URL != "https://api.example.com/v1/ping" {
			t.Errorf("请求行 = %s %s", dto.Request.Method, dto.Request.URL)
		}
		if dto.Response == nil || dto.Response.Status != http.StatusCreated {
			t.Errorf("响应 = %+v,期望 status 201", dto.Response)
		}
	})

	t.Run("未命中回 JSON 信封", func(t *testing.T) {
		rec := do(t, mux, http.MethodGet, "/api/sessions/nope", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d,期望 404", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Success || e.Message != "session not found" {
			t.Errorf("响应 = success:%v message:%q", e.Success, e.Message)
		}
	})
}

// TestSessionDeleteEffectAndIdempotence 删除移除目标会话并返回无 data 信封；重复删除保持幂等。
func TestSessionDeleteEffectAndIdempotence(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	s.svc.RecordFlowCompleted(newFlowFixture("del-1"))

	rec := do(t, mux, http.MethodDelete, "/api/sessions/del-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	if got := bodyKeys(t, rec); !slices.Equal(got, []string{"success", "timestamp"}) {
		t.Errorf("响应键 = %v,期望恰为 {success,timestamp}", got)
	}
	if !decodeEnvelope(t, rec).Success {
		t.Error("success 应为 true")
	}

	if got := do(t, mux, http.MethodGet, "/api/sessions/del-1", ""); got.Code != http.StatusNotFound {
		t.Errorf("删除后再取详情 = %d,期望 404", got.Code)
	}
	if _, total := s.svc.Sessions(1, 50); total != 0 {
		t.Errorf("删除后总数 = %d,期望 0", total)
	}

	// 重复删除返回成功，适配环形存储淘汰和多窗口操作。
	if got := do(t, mux, http.MethodDelete, "/api/sessions/del-1", ""); got.Code != http.StatusOK {
		t.Errorf("重复删除 = %d,期望 200(幂等)", got.Code)
	}
}

// TestSessionIDSeparatorVariantsKeepParent ID 中的分隔符和多余路径段返回 404，并保持父会话存在。
func TestSessionIDSeparatorVariantsKeepParent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"编码过的分隔符", http.MethodDelete, "/api/sessions/abc%2Fjunk"},
		{"尾斜杠", http.MethodDelete, "/api/sessions/abc/"},
		{"多余路径段", http.MethodDelete, "/api/sessions/abc/junk"},
		{"多余段在前", http.MethodGet, "/api/sessions/abc/junk/body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			s.svc.RecordFlowCompleted(newFlowFixture("abc"))

			rec := do(t, mux, c.method, c.path, "")
			if rec.Code != http.StatusNotFound {
				t.Errorf("状态码 = %d,期望 404", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "unknown action" {
				t.Errorf("message = %q,期望 \"unknown action\"", e.Message)
			}
			if got := do(t, mux, http.MethodGet, "/api/sessions/abc", ""); got.Code != http.StatusOK {
				t.Errorf("父会话被动了:再取详情 = %d", got.Code)
			}
		})
	}

	// URL.Path 将 %2F 解码为 /，编码和未编码写法在处理器中保持等价。
	t.Run("编码过的 body 后缀与未编码等价", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		s.svc.RecordFlowCompleted(newFlowFixture("abc",
			withResponse(http.StatusOK, "audio/mpeg", []byte("0123456789"))))

		encoded := do(t, mux, http.MethodGet, "/api/sessions/abc%2Fbody", "")
		plain := do(t, mux, http.MethodGet, "/api/sessions/abc/body", "")
		// 只比较 data，排除秒级 timestamp 的时间差。
		if encoded.Code != plain.Code {
			t.Errorf("状态码不同:%d / %d", encoded.Code, plain.Code)
		}
		if a, b := decodeEnvelope(t, encoded), decodeEnvelope(t, plain); string(a.Data) != string(b.Data) {
			t.Errorf("两种写法的 data 不同:%s / %s", a.Data, b.Data)
		}
	})
}

// TestSessionListEnvelopeShape 会话列表使用 paginatedResponse，并返回分页字段与列表数据。
func TestSessionListEnvelopeShape(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	for _, id := range []string{"Flow-A", "Flow-B", "Flow-C"} {
		s.svc.RecordFlowCompleted(newFlowFixture(id))
	}

	rec := do(t, mux, http.MethodGet, "/api/sessions?page=2&pageSize=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if got := bodyKeys(t, rec); !slices.Equal(got, []string{"data", "hasNext", "hasPrev", "page", "pageSize", "total"}) {
		t.Errorf("顶层键 = %v,期望分页信封的六个字段(不含 success/message/timestamp)", got)
	}
	page := decodePage(t, rec)
	var list []service.HTTPSessionDTO
	if err := json.Unmarshal(page.Data, &list); err != nil {
		t.Fatalf("解析列表失败: %v", err)
	}
	if len(list) != 1 || page.Total != 3 || page.Page != 2 || page.PageSize != 2 || page.HasNext || !page.HasPrev {
		t.Errorf("末页 = len:%d %+v", len(list), page)
	}
}

// TestSessionsClearEmptiesStore 清空端点移除所有会话，列表和详情随后均反映空状态。
func TestSessionsClearEmptiesStore(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	for _, id := range []string{"Flow-A", "Flow-B"} {
		s.svc.RecordFlowCompleted(newFlowFixture(id))
	}

	rec := do(t, mux, http.MethodPost, "/api/sessions/clear", "")
	if rec.Code != http.StatusOK || !decodeEnvelope(t, rec).Success {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}

	page := decodePage(t, do(t, mux, http.MethodGet, "/api/sessions", ""))
	if page.Total != 0 {
		t.Errorf("清空后 total = %d,期望 0", page.Total)
	}
	if string(page.Data) != "[]" {
		t.Errorf("清空后 data = %s,期望 [](不是 null)", page.Data)
	}
	if got := do(t, mux, http.MethodGet, "/api/sessions/Flow-A", ""); got.Code != http.StatusNotFound {
		t.Errorf("清空后取详情 = %d,期望 404", got.Code)
	}
}

// TestSessionEmptyIDRejected 空会话 ID 返回 400，真实 mux 的双斜杠继续由 cleanPath 重定向。
func TestSessionEmptyIDRejected(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t)
	s.svc.RecordFlowCompleted(newFlowFixture("Flow-A"))
	// 直调处理器覆盖 mux cleanPath 之前的空 ID 形态。
	h := http.HandlerFunc(s.handleSession)

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/sessions/"},
		{http.MethodDelete, "/api/sessions/"},
		{http.MethodGet, "/api/sessions//body"},
		{http.MethodGet, "/api/sessions//body/raw"},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := do(t, h, c.method, c.path, "")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d,期望 400", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "invalid session id" {
				t.Errorf("message = %q,期望 \"invalid session id\"", e.Message)
			}
		})
	}
	if _, total := s.svc.Sessions(1, 50); total != 1 {
		t.Errorf("空 id 的 DELETE 不该动到任何会话,剩余 %d 条", total)
	}

	// 真实 mux 先由 cleanPath 折叠双斜杠并返回 307。
	t.Run("经 mux 时双斜杠被重定向", func(t *testing.T) {
		_, mux := newTestServer(t)
		rec := do(t, mux, http.MethodGet, "/api/sessions//body", "")
		if rec.Code != http.StatusTemporaryRedirect {
			t.Errorf("状态码 = %d,期望 307", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/api/sessions/body" {
			t.Errorf("Location = %q,期望 \"/api/sessions/body\"", got)
		}
	})
}

// TestWSAndStreamSessionDetail WebSocket 与流式详情返回对应 ID、消息列表和未命中 404。
func TestWSAndStreamSessionDetail(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)

	now := fixtureTime
	s.svc.RecordWSSession(&flow.WSSession{
		ID:        "ws-1",
		URL:       "wss://api.example.com/socket",
		Status:    "open",
		StartTime: now,
		Messages: []flow.WSMessage{
			{ID: "m1", FlowID: "ws-1", Direction: "client->server", Type: "text", Data: []byte("hi"), Timestamp: now},
		},
		MessageCount: 1,
	}, nil)
	s.svc.RecordStreamSession(&flow.StreamSession{
		ID:        "st-1",
		URL:       "https://api.example.com/events",
		Kind:      "sse",
		Status:    "open",
		StartTime: now,
		Messages: []flow.StreamMessage{
			{ID: "s1", FlowID: "st-1", Direction: "server->client", Kind: "sse", Data: []byte("tick"), Timestamp: now.Add(time.Second)},
		},
		MessageCount: 1,
	}, nil)

	for _, c := range []struct {
		name string
		base string
		id   string
	}{
		{"websocket-sessions", "/api/websocket-sessions", "ws-1"},
		{"stream-sessions", "/api/stream-sessions", "st-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, mux, http.MethodGet, c.base+"/"+c.id, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("详情状态码 = %d,响应 %s", rec.Code, rec.Body.String())
			}
			var detail struct {
				ID       string            `json:"id"`
				Messages []json.RawMessage `json:"messages"`
			}
			decodeEnvelope(t, rec).into(t, &detail)
			if detail.ID != c.id {
				t.Errorf("data.id = %q,期望 %q", detail.ID, c.id)
			}
			if len(detail.Messages) != 1 {
				t.Errorf("消息条数 = %d,期望 1", len(detail.Messages))
			}

			miss := do(t, mux, http.MethodGet, c.base+"/nope", "")
			if miss.Code != http.StatusNotFound {
				t.Errorf("未命中状态码 = %d,期望 404", miss.Code)
			}
			if e := decodeEnvelope(t, miss); e.Success || e.Message != "session not found" {
				t.Errorf("未命中响应 = success:%v message:%q", e.Success, e.Message)
			}

			page := decodePage(t, do(t, mux, http.MethodGet, c.base, ""))
			if page.Total != 1 {
				t.Errorf("列表 total = %d,期望 1", page.Total)
			}
			var list []json.RawMessage
			if err := json.Unmarshal(page.Data, &list); err != nil || len(list) != 1 {
				t.Errorf("列表 data = %s (err %v)", page.Data, err)
			}
		})
	}
}
