// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

func TestSessionArchiveAPIRoundTrip(t *testing.T) {
	t.Parallel()
	_, source := newExportServer(t)
	export := do(t, source, http.MethodPost, "/api/export", `{"format":"har","sessionIds":["Flow-A"]}`)
	if export.Code != http.StatusOK || export.Header().Get("Content-Disposition") != `attachment; filename="sessions.har"` {
		t.Fatalf("HAR 导出: %d, %s", export.Code, export.Body.String())
	}
	svc, target := newTestServer(t)
	svc.svc.StopRecording()
	for i := 0; i < 2; i++ {
		imported := do(t, target, http.MethodPost, "/api/import", export.Body.String())
		var response struct {
			Success bool                        `json:"success"`
			Data    service.SessionImportResult `json:"data"`
		}
		if err := json.Unmarshal(imported.Body.Bytes(), &response); err != nil || imported.Code != http.StatusOK || !response.Success || response.Data.HTTP != 1 {
			t.Fatalf("导入: %d, %s (%v)", imported.Code, imported.Body.String(), err)
		}
	}
	ids := svc.svc.SessionIDs()
	if len(ids) != 2 || ids[0] == ids[1] || ids[0] == "Flow-A" {
		t.Fatalf("导入应追加并分配独立 ID: %v", ids)
	}
	f, _ := svc.svc.RawFlow(ids[0])
	if string(f.Response.Body) != "response-Flow-A" {
		t.Fatal("正文未还原")
	}
}

func TestSessionImportInvalidRequests(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "{}", "[]", `{"log":{"entries":null}}`, `{"log":{"entries":[{}]}}`, `{"log":{"entries":[]}} {}`} {
		t.Run(body, func(t *testing.T) {
			s, mux := newTestServer(t)
			rec := do(t, mux, http.MethodPost, "/api/import", body)
			if rec.Code != http.StatusBadRequest || len(s.svc.SessionIDs()) != 0 {
				t.Fatalf("非法导入: %d, %s", rec.Code, rec.Body.String())
			}
		})
	}
	_, mux := newTestServer(t)
	empty := do(t, mux, http.MethodPost, "/api/import", `{"log":{"version":"1.2","entries":[]}}`)
	if empty.Code != http.StatusOK {
		t.Fatalf("空档案: %d, %s", empty.Code, empty.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
		rec := do(t, mux, method, "/api/import", "{}")
		if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "POST") {
			t.Fatalf("导入方法白名单: %s => %d", method, rec.Code)
		}
	}
	unknown := do(t, mux, http.MethodPost, "/api/import/extra", "{}")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("多余路径: %d", unknown.Code)
	}
}
