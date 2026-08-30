// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/service"
)

// 本文件对应 export.go 的过滤器与流式写出。导出的内容含请求头里的 Cookie/Authorization 明文,
// 过滤条件放宽一档就等于把整份抓包会话落盘外发。

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

// exportIDs 发一次导出并返回结果里的会话 ID(已排序,便于按集合比对)。
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

// TestHandleExportFiltersSessions 组合过滤 + 下载头。头断言取 Result().Header(首次 Write 时的快照)
// 而不是实时 map:把四个 Header().Set 挪到写循环之后,取实时 map 的断言照样绿,而真实客户端
// 拿到的是隐式 200 + 无 Content-Disposition 的响应 —— 浏览器不再触发下载,
// 而是把上兆 JSON(含 Cookie、Authorization 明文)直接渲染在页面里。
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

// TestHandleExportDefaultsToAllJSONWithBodies 不带过滤条件时导出全部,并保持最新优先。
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

// TestExportEmptyFilterValuesDoNotFilter stringSet 的「空值跳过」与「全空→nil」两个分支决定了
// 一个把输入框空值原样塞进数组的前端改动,会让「只导出选中的这 2 条」变成把整份抓包会话
// 全量落盘外发 —— 用户看到一份自己从没勾选过的导出文件。
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

// TestExportTimeRangeBoundaries 边界从 !Before/!After 改成 After/Before 后,正好落在起止时刻的
// 那条会话会静默消失 —— 用户按界面显示的时间戳复制起止时间去导出,恰恰漏掉自己要查的那一条。
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

// TestExportHostFilterSemantics 界面上会话列表显示 "api.example.com:443",用户复制粘贴进导出
// 过滤器得到零条结果 —— 这里的不对称最容易踩。把方向改反则会让所有带端口的会话突然全部漏掉。
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
		// 反方向不成立:过滤条件带端口时,裸域的会话匹配不上。
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

// TestExportStatusCodeFilterSkipsPendingSessions HasResponse 判空被删后,用户按「只导出 500」
// 筛选会额外得到一批还没拿到响应的挂起请求,导出结果与界面上看到的筛选结果对不上。
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

// TestExportZeroMatchAndClientCancel first/逗号 的手写拼接没有零命中用例:把 "[" 的写入挪进循环,
// 零命中就会输出空响应体,前端 JSON.parse 抛异常,用户点「导出」得到 0 字节文件却没有错误提示。
// ctx 取消那支则是用户导出大批会话时点取消/关标签的唯一止损。
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

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, testHost+"/api/export", strings.NewReader(`{}`)).WithContext(ctx)
		s.handleExport(rec, req)

		// 已取消时循环第一轮就退出:只写出了开头的 "[",没有收尾的 "]\n"。
		if got := rec.Body.String(); got != "[" {
			t.Errorf("响应体 = %q,期望只有 \"[\"(早退,不再序列化任何会话)", got)
		}
	})
}

// TestHandleExportRejectsInvalidRequests 过滤条件解析失败必须是 400 而不是导出全部。
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

// TestHandleExportRejectsOversizedRequest 过滤条件本身也有上限:没有它,一个无限大的请求体
// 在解码阶段就能把内存吃光。
func TestHandleExportRejectsOversizedRequest(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, body string }{
		{"超大字段值", `{"format":"` + strings.Repeat("x", int(maxSessionExportRequestBytes)) + `"}`},
		// 尾随空白同样计入上限:否则「合法 JSON + 无限空白」能绕过这道闸门。
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
