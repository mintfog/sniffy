// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

func mediaService(t *testing.T, id string, data []byte) *service.Service {
	t.Helper()
	svc := service.New(nil, nil, "", "")
	f := flow.New(flow.ProtoHTTPS)
	f.ID = id
	f.State = flow.StateCompleted
	f.Request = &flow.Request{Method: "GET", URL: "https://x/a.mp4", Header: map[string][]string{}}
	f.Response = &flow.Response{
		Status: 200,
		Header: map[string][]string{"Content-Type": {"video/mp4"}},
		Body:   data,
	}
	svc.RecordFlowCompleted(f)
	return svc
}

// TestBodyRouteHandler Wails 会先把路由前缀从 Path 上摘掉,handler 收到的是「/{id}」;
// 摘前缀的约定一旦变化,音视频预览会静默 404,故在此钉住。
func TestBodyRouteHandler(t *testing.T) {
	h := &bodyRouteHandler{svc: mediaService(t, "abc123", []byte("0123456789"))}

	req := httptest.NewRequest(http.MethodGet, "/abc123?source=response", nil)
	req.Header.Set("Range", "bytes=0-3")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("状态码应为 206,实际 %d", rec.Code)
	}
	if got := rec.Body.String(); got != "0123" {
		t.Fatalf("分片内容不对: %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("MIME 不对: %q", ct)
	}
}

// TestBodyRouteHandlerRejectsBadPath 无 id 或多级路径都不是合法请求,直接 404。
func TestBodyRouteHandlerRejectsBadPath(t *testing.T) {
	h := &bodyRouteHandler{svc: mediaService(t, "abc123", []byte("data"))}
	for _, path := range []string{"/", "/abc123/extra"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s 应回 404,实际 %d", path, rec.Code)
		}
	}
}
