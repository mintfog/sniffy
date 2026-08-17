// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

func exportTestFlow(id, method, host string, status int, at time.Time, requestBody, responseBody string) *flow.Flow {
	f := flow.New(flow.ProtoHTTPS)
	f.ID = id
	f.State = flow.StateCompleted
	f.Timing.RequestAt = at
	f.Timing.ResponseAt = at.Add(25 * time.Millisecond)
	f.Timing.CompletedAt = at.Add(50 * time.Millisecond)
	f.Timing.DurationMs = 50
	f.Request = &flow.Request{
		Method: method,
		URL:    "https://" + host + "/resource",
		Host:   host,
		Path:   "/resource",
		Header: map[string][]string{"Content-Type": {"text/plain"}},
		Body:   []byte(requestBody),
	}
	f.Response = &flow.Response{
		Status: status,
		Header: map[string][]string{"Content-Type": {"text/plain"}},
		Body:   []byte(responseBody),
	}
	return f
}

func exportTestServer(t *testing.T) *Server {
	t.Helper()
	svc := service.New(nil, nil, "", "")
	base := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	svc.RecordFlowCompleted(exportTestFlow("Flow-A", "GET", "API.Example.com:443", 200, base, "request-a", "response-a"))
	svc.RecordFlowCompleted(exportTestFlow("Flow-B", "POST", "api.example.com", 500, base.Add(time.Hour), "request-b", "response-b"))
	svc.RecordFlowCompleted(exportTestFlow("Flow-C", "GET", "other.example.com", 200, base.Add(2*time.Hour), "request-c", "response-c"))
	return &Server{svc: svc}
}

func TestHandleExportFiltersSessions(t *testing.T) {
	s := exportTestServer(t)
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
	rec := httptest.NewRecorder()
	s.handleExport(rec, httptest.NewRequest(http.MethodPost, "/api/export", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("导出状态码 = %d,响应: %s", rec.Code, rec.Body.String())
	}
	var sessions []service.HTTPSessionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("导出结果不是合法 JSON 数组: %v (%s)", err, rec.Body.String())
	}
	if len(sessions) != 1 || sessions[0].ID != "Flow-A" {
		t.Fatalf("组合过滤结果 = %+v,期望仅 Flow-A", sessions)
	}
	if sessions[0].Request.Body != "" {
		t.Fatalf("请求 Body 应被排除,实际 %q", sessions[0].Request.Body)
	}
	if sessions[0].Response == nil || sessions[0].Response.Body != "response-a" {
		t.Fatalf("响应 Body 应保留,实际 %+v", sessions[0].Response)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="sessions.json"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestHandleExportDefaultsToAllJSONWithBodies(t *testing.T) {
	s := exportTestServer(t)
	rec := httptest.NewRecorder()
	s.handleExport(rec, httptest.NewRequest(http.MethodPost, "/api/export", nil))

	var sessions []service.HTTPSessionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("解析默认导出失败: %v (%s)", err, rec.Body.String())
	}
	if len(sessions) != 3 {
		t.Fatalf("默认导出数量 = %d,期望 3", len(sessions))
	}
	if sessions[0].ID != "Flow-C" || sessions[0].Request.Body != "request-c" || sessions[0].Response == nil || sessions[0].Response.Body != "response-c" {
		t.Fatalf("默认导出应保持最新优先并包含 Body: %+v", sessions[0])
	}
}

func TestHandleExportRejectsInvalidRequests(t *testing.T) {
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
			s := exportTestServer(t)
			rec := httptest.NewRecorder()
			s.handleExport(rec, httptest.NewRequest(tt.method, "/api/export", strings.NewReader(tt.body)))
			if rec.Code != tt.status {
				t.Fatalf("状态码 = %d,期望 %d,响应: %s", rec.Code, tt.status, rec.Body.String())
			}
		})
	}
}

func TestHandleExportRejectsOversizedRequest(t *testing.T) {
	s := exportTestServer(t)
	body := `{"format":"` + strings.Repeat("x", int(maxSessionExportRequestBytes)) + `"}`
	rec := httptest.NewRecorder()
	s.handleExport(rec, httptest.NewRequest(http.MethodPost, "/api/export", strings.NewReader(body)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求状态码 = %d,期望 413,响应: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleExportRejectsOversizedTrailingWhitespace(t *testing.T) {
	s := exportTestServer(t)
	body := `{}` + strings.Repeat(" ", int(maxSessionExportRequestBytes))
	rec := httptest.NewRecorder()
	s.handleExport(rec, httptest.NewRequest(http.MethodPost, "/api/export", strings.NewReader(body)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("尾随空白超限状态码 = %d,期望 413,响应: %s", rec.Code, rec.Body.String())
	}
}
