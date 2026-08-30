// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/service"
)

// 本文件覆盖 export.go 的过滤器与流式写出；导出内容可能包含请求头中的 Cookie/Authorization。

// newExportServer 造一台带四条会话的服务器:
// Flow-A(GET api.example.com:443 / 200 / base)、Flow-B(POST api.example.com / 500 / base+1h)、
// Flow-C(GET other.example.com / 200 / base+2h)、Flow-Z(时刻为零值)。
func newExportServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	s, mux := newTestServer(t)
	for _, f := range []*flowFixtureSpec{
		{"Flow-A", http.MethodGet, "API.Example.com:443", http.StatusOK, fixtureTime},
		{"Flow-B", http.MethodPost, "api.example.com", http.StatusInternalServerError, fixtureTime.Add(time.Hour)},
		{"Flow-C", http.MethodGet, "other.example.com", http.StatusOK, fixtureTime.Add(2 * time.Hour)},
		{"Flow-Z", http.MethodGet, "zero.example.com", http.StatusOK, time.Time{}},
	} {
		s.svc.RecordFlowCompleted(newFlowFixture(f.id,
			withRequest(f.method, "https://"+f.host+"/resource", f.host),
			withRequestBody("text/plain", []byte("request-"+f.id)),
			withResponse(f.status, "text/plain", []byte("response-"+f.id)),
			withRequestAt(f.at),
		))
	}
	return s, mux
}

type flowFixtureSpec struct {
	id     string
	method string
	host   string
	status int
	at     time.Time
}

// exportIDs 发起一次导出并返回排序后的会话 ID。
func exportIDs(t *testing.T, mux http.Handler, body string) []string {
	t.Helper()
	rec := do(t, mux, http.MethodPost, "/api/export", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("导出状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var sessions []service.HTTPSessionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("导出结果不是合法 JSON 数组: %v (%s)", err, rec.Body.String())
	}
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	return ids
}

// TestHandleExportFiltersSessions 组合过滤条件并验证下载响应头；Header 取首次 Write 时的快照。
func TestHandleExportFiltersSessions(t *testing.T) {
	t.Parallel()
	_, mux := newExportServer(t)
	body := `{
		"format":"JSON",
		"sessionIds":["Flow-A","Flow-C"],
		"methods":["get"],
		"hosts":["api.example.com"],
		"statusCodes":[200],
		"timeRange":{"start":"2026-08-18T09:30:00Z","end":"2026-08-18T10:30:00Z"},
		"includeRequestBody":false,
		"includeResponseBody":true
	}`
	rec := do(t, mux, http.MethodPost, "/api/export", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}

	var sessions []service.HTTPSessionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("导出结果不是合法 JSON 数组: %v (%s)", err, rec.Body.String())
	}
	if len(sessions) != 1 || sessions[0].ID != "Flow-A" {
		t.Fatalf("组合过滤结果 = %+v,期望仅 Flow-A", sessions)
	}
	if sessions[0].Request.Body != "" {
		t.Errorf("请求 Body 应被排除,实际 %q", sessions[0].Request.Body)
	}
	if sessions[0].Response == nil || sessions[0].Response.Body != "response-Flow-A" {
		t.Errorf("响应 Body 应保留,实际 %+v", sessions[0].Response)
	}

	header := rec.Result().Header
	for _, c := range []struct{ key, want string }{
		{"Content-Type", "application/json"},
		{"Content-Disposition", `attachment; filename="sessions.json"`},
		{"Cache-Control", "no-store"},
		{"X-Content-Type-Options", "nosniff"},
	} {
		if got := header.Get(c.key); got != c.want {
			t.Errorf("%s = %q,期望 %q", c.key, got, c.want)
		}
	}
}

// TestHandleExportDefaultsToAllJSONWithBodies 缺省导出全部会话，按最新优先并包含请求与响应体。
func TestHandleExportDefaultsToAllJSONWithBodies(t *testing.T) {
	t.Parallel()
	_, mux := newExportServer(t)
	rec := do(t, mux, http.MethodPost, "/api/export", "")

	var sessions []service.HTTPSessionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("解析默认导出失败: %v (%s)", err, rec.Body.String())
	}
	if len(sessions) != 4 {
		t.Fatalf("默认导出数量 = %d,期望 4", len(sessions))
	}
	first := sessions[0]
	if first.ID != "Flow-Z" || first.Request.Body != "request-Flow-Z" ||
		first.Response == nil || first.Response.Body != "response-Flow-Z" {
		t.Errorf("默认导出应保持最新优先并包含 Body: %+v", first)
	}
}

// TestExportEmptyFilterValuesDoNotFilter 过滤数组中的空白项被忽略；全部为空时表示不设过滤条件。
func TestExportEmptyFilterValuesDoNotFilter(t *testing.T) {
	t.Parallel()
	all := []string{"Flow-A", "Flow-B", "Flow-C", "Flow-Z"}
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"全空 sessionIds 降级成不过滤", `{"sessionIds":["  "]}`, all},
		{"全空 methods 降级成不过滤", `{"methods":[""]}`, all},
		{"全空 hosts 降级成不过滤", `{"hosts":[" ",""]}`, all},
		{"混入空值时仍按非空项过滤", `{"sessionIds":["Flow-A","  "]}`, []string{"Flow-A"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, mux := newExportServer(t)
			if got := exportIDs(t, mux, c.body); !slices.Equal(got, c.want) {
				t.Errorf("导出 = %v,期望 %v", got, c.want)
			}
		})
	}
}

// TestExportMethodFilter methods 支持大小写不敏感的单选、多选与零命中结果。
func TestExportMethodFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []string
	}{
		// 大小写归一:界面上显示的是 GET/POST,而调用方手写小写是常态。
		{"只导出 POST", `{"methods":["post"]}`, []string{"Flow-B"}},
		{"只导出 GET", `{"methods":["GET"]}`, []string{"Flow-A", "Flow-C", "Flow-Z"}},
		{"多选取并集", `{"methods":["get","post"]}`, []string{"Flow-A", "Flow-B", "Flow-C", "Flow-Z"}},
		{"无人命中的方法回空数组", `{"methods":["DELETE"]}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, mux := newExportServer(t)
			got := exportIDs(t, mux, c.body)
			if len(got) != len(c.want) || (len(c.want) > 0 && !slices.Equal(got, c.want)) {
				t.Errorf("导出 = %v,期望 %v", got, c.want)
			}
		})
	}
}

// TestExportTimeRangeBoundaries 时间范围起止端点均为闭区间，零值时间仅在未设置范围时参与导出。
func TestExportTimeRangeBoundaries(t *testing.T) {
	t.Parallel()
	base := fixtureTime.Format(time.RFC3339)
	cases := []struct {
		name string
		body string
		want []string
	}{
		// 起点闭区间:恰在 base 的 Flow-A 必须在内;RequestAt 为零值的 Flow-Z 一律排除。
		{"只给 start", `{"timeRange":{"start":"` + base + `"}}`, []string{"Flow-A", "Flow-B", "Flow-C"}},
		{"只给 end", `{"timeRange":{"end":"` + base + `"}}`, []string{"Flow-A"}},
		{"不给 timeRange 时零值会话也在内", `{}`, []string{"Flow-A", "Flow-B", "Flow-C", "Flow-Z"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, mux := newExportServer(t)
			if got := exportIDs(t, mux, c.body); !slices.Equal(got, c.want) {
				t.Errorf("导出 = %v,期望 %v", got, c.want)
			}
		})
	}
}

// TestExportHostFilterSemantics 裸域过滤命中带端口和裸域主机；带端口条件只匹配相同端口形式，IPv6 保持括号语义。
func TestExportHostFilterSemantics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		host    string
		filters []string
		want    bool
	}{
		{"过滤裸域命中带端口的 host", "API.Example.com:443", []string{"api.example.com"}, true},
		{"过滤裸域命中裸域", "api.example.com", []string{"api.example.com"}, true},
		// 带端口条件要求主机包含相同端口。
		{"过滤带端口不命中裸域", "api.example.com", []string{"api.example.com:443"}, false},
		{"IPv6 裸地址命中带端口", "[::1]:8080", []string{"::1"}, true},
		{"IPv6 带方括号的过滤条件不命中", "[::1]:8080", []string{"[::1]"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			filters := make(map[string]struct{}, len(c.filters))
			for _, f := range c.filters {
				filters[strings.ToLower(f)] = struct{}{}
			}
			if got := matchSessionExportHost(c.host, filters); got != c.want {
				t.Errorf("matchSessionExportHost(%q, %v) = %v,期望 %v", c.host, c.filters, got, c.want)
			}
		})
	}
}

// TestExportStatusCodeFilterSkipsPendingSessions 状态码过滤排除尚无响应的挂起会话；未设置状态码过滤时保留全部会话。
func TestExportStatusCodeFilterSkipsPendingSessions(t *testing.T) {
	t.Parallel()
	newServer := func(t *testing.T) http.Handler {
		s, mux := newExportServer(t)
		s.svc.RecordFlowStarted(newFlowFixture("Flow-P", withoutResponse()))
		return mux
	}
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"按状态码筛选排除挂起会话", `{"statusCodes":[200]}`, []string{"Flow-A", "Flow-C", "Flow-Z"}},
		{"不筛状态码时挂起会话在内", `{}`, []string{"Flow-A", "Flow-B", "Flow-C", "Flow-P", "Flow-Z"}},
		{"无人命中的状态码回空数组", `{"statusCodes":[404]}`, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := exportIDs(t, newServer(t), c.body)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("导出 = %v,期望 %v", got, c.want)
			}
		})
	}
}

// TestExportZeroMatchAndClientCancel 零命中仍输出合法空数组；客户端取消后立即停止流式写出。
func TestExportZeroMatchAndClientCancel(t *testing.T) {
	t.Parallel()

	t.Run("零命中仍是合法空数组", func(t *testing.T) {
		t.Parallel()
		_, mux := newExportServer(t)
		rec := do(t, mux, http.MethodPost, "/api/export", `{"sessionIds":["no-such-id"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		if got := rec.Body.String(); got != "[]\n" {
			t.Errorf("响应体 = %q,期望 \"[]\\n\"", got)
		}
		var sessions []service.HTTPSessionDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil || len(sessions) != 0 {
			t.Errorf("零命中结果 = %v (err %v)", sessions, err)
		}
		if got := rec.Result().Header.Get("Content-Disposition"); got != `attachment; filename="sessions.json"` {
			t.Errorf("零命中也应带下载头,got %q", got)
		}
	})

	t.Run("客户端中途断开即停止写出", func(t *testing.T) {
		t.Parallel()
		s, _ := newExportServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := do(t, http.HandlerFunc(s.handleExport), http.MethodPost, "/api/export", `{}`, withCtx(ctx))

		// 已取消时循环在首轮退出，响应保留已写出的起始字节。
		if got := rec.Body.String(); got != "[" {
			t.Errorf("响应体 = %q,期望只有 \"[\"(早退,不再序列化任何会话)", got)
		}
	})
}

// TestHandleExportRejectsInvalidRequests 过滤条件解析失败统一返回 400。
func TestHandleExportRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		body   string
		status int
	}{
		{"只允许 POST", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"不支持的格式", http.MethodPost, `{"format":"har"}`, http.StatusBadRequest},
		{"开始时间非法", http.MethodPost, `{"timeRange":{"start":"yesterday"}}`, http.StatusBadRequest},
		{"结束时间非法", http.MethodPost, `{"timeRange":{"end":"tomorrow"}}`, http.StatusBadRequest},
		{"时间范围倒置", http.MethodPost, `{"timeRange":{"start":"2026-08-18T12:00:00Z","end":"2026-08-18T10:00:00Z"}}`, http.StatusBadRequest},
		{"状态码非法", http.MethodPost, `{"statusCodes":[99]}`, http.StatusBadRequest},
		{"未知字段", http.MethodPost, `{"methdos":["GET"]}`, http.StatusBadRequest},
		{"JSON 非法", http.MethodPost, `{`, http.StatusBadRequest},
		{"多个 JSON 值", http.MethodPost, `{} {}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, mux := newExportServer(t)
			rec := do(t, mux, tt.method, "/api/export", tt.body)
			if rec.Code != tt.status {
				t.Fatalf("状态码 = %d,期望 %d,响应: %s", rec.Code, tt.status, rec.Body.String())
			}
		})
	}
}

// TestHandleExportRejectsOversizedRequest 过滤请求体包含字段值和尾随空白在内的大小上限。
func TestHandleExportRejectsOversizedRequest(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, body string }{
		{"超大字段值", `{"format":"` + strings.Repeat("x", int(maxSessionExportRequestBytes)) + `"}`},
		// 尾随空白计入请求体上限。
		{"尾随空白超限", `{}` + strings.Repeat(" ", int(maxSessionExportRequestBytes))},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, mux := newExportServer(t)
			rec := do(t, mux, http.MethodPost, "/api/export", c.body)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("状态码 = %d,期望 413,响应: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
