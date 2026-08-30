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

// 本文件承担 sessions.go 的非 body 部分:详情、删除、清空、列表信封、id 解析,
// 以及 WebSocket / 流式两条兄弟端点。/body 与 /body/raw 的契约在 sessionbody_test.go。

// TestSessionDetailEnvelope 这个端点的成功分支此前执行计数为 0:全包测试从没让它成功返回过一条会话。
// 信封换成 paginated 或 data 换成 metadata 都不会失败,headless 客户端拿到 200 却解不出 data.id。
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

// TestSessionDeleteEffectAndIdempotence 覆盖率只证明这两行被方法矩阵执行过,没有任何断言证明
// DeleteSession 真被调用。接错成别的 store 时,用户在 headless 端点上点删除拿到 200 却发现会话还在
// (内容含 Cookie/Token,用户以为已清除)。
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

	// 幂等是刻意的:会话可能已被环形存储淘汰,重复删除报 404 会让前端弹一个无从解释的错。
	if got := do(t, mux, http.MethodDelete, "/api/sessions/del-1", ""); got.Code != http.StatusOK {
		t.Errorf("重复删除 = %d,期望 200(幂等)", got.Code)
	}
}

// TestSessionIDSeparatorVariantsKeepParent 这道关口的注释点名要防「多余段并进 id 后 DELETE 回 200
// 却删了个空」。守卫改成先 path.Clean 或对已解码路径漏判时,尾斜杠正是唯一能造成误删父资源的形态。
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

	// %2F 在 URL.Path 里已被解码成 "/",与未编码写法在 handler 眼里是同一条路径 ——
	// 这正是上面那格能生效的前提,单独钉一下免得有人改成读 RawPath 后自以为等价。
	t.Run("编码过的 body 后缀与未编码等价", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		s.svc.RecordFlowCompleted(newFlowFixture("abc",
			withResponse(http.StatusOK, "audio/mpeg", []byte("0123456789"))))

		encoded := do(t, mux, http.MethodGet, "/api/sessions/abc%2Fbody", "")
		plain := do(t, mux, http.MethodGet, "/api/sessions/abc/body", "")
		if encoded.Code != plain.Code || encoded.Body.String() != plain.Body.String() {
			t.Errorf("两种写法结果不同:%d %s / %d %s",
				encoded.Code, encoded.Body.String(), plain.Code, plain.Body.String())
		}
	})
}

// TestSessionListEnvelopeShape 同一套 REST 有两种响应形状(apiResponse 与 paginatedResponse),
// 调用方按端点分别解析。把 paginated(w,...) 改成 ok(w,list) 是一处极自然的「统一信封」重构,
// 客户端的分页与总数显示会一起失效而测试全绿。
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

// TestSessionsClearEmptiesStore 这是全站破坏性最强也最常用的一次调用,而现有用例跑在空 service 上,
// handler 换成空实现照样通过。「点了清空但列表还在」或「清空的是别的 store」只能靠端点级断言兜住。
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

// TestSessionEmptyIDRejected 三处空 id 守卫此前执行计数全为 0。守卫被挪走后 DELETE /api/sessions/
// 会落进 DeleteSession("") 并回 200,调用方以为删掉了什么。
func TestSessionEmptyIDRejected(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t)
	s.svc.RecordFlowCompleted(newFlowFixture("Flow-A"))
	// 直调 handler:mux 的 cleanPath 会把 // 折叠掉,这几条形态到不了处理器。
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

	// 走 mux 时结论不同:cleanPath 先把 // 折叠成 / 并回 307。写清楚免得下一个人
	// 以为直调与走 mux 之中有一层坏了。
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

// TestWSAndStreamSessionDetail 这两个端点的成功返回从未执行过 —— 现有测试只用不存在的 id 打过它们。
// 取数接错或 DTO 字段改名后,WS 详情页与流式回放整块空白,而测试仍然全绿。
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
