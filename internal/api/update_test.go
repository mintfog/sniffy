// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/update"
)

func TestUpdateRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/api/update/skip", "/api/update/auto"} {
		for _, body := range []string{`{`, `{"enabled":"false","version":123}`, strings.Repeat("x", maxUpdateBodyBytes+1)} {
			_, mux := newTestServer(t)
			rec := do(t, mux, http.MethodPost, path, body)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("%s 接受了非法请求：%d %s", path, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestUpdateDownloadOutlivesStartRequest(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	release := make(chan struct{})
	finishDownload := sync.OnceFunc(func() { close(release) })
	defer finishDownload()
	pointUpdateFeedWith(t, s, "1.4.0", func(r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	do(t, mux, http.MethodPost, "/api/update/check", "")
	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	cancel()
	finishDownload()
	if rec.Code != http.StatusOK {
		t.Fatalf("下载请求失败：%d %s", rec.Code, rec.Body.String())
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state service.UpdateStateDTO
		decodeEnvelope(t, do(t, mux, http.MethodGet, "/api/update", "")).into(t, &state)
		if state.Status == service.UpdateStatusDownloaded {
			if state.DownloadedPath == "" {
				t.Fatal("下载成功但缺少本地路径")
			}
			return
		}
		if state.Status == service.UpdateStatusError {
			t.Fatalf("后台下载失败：%s", state.Error)
		}
		select {
		case <-deadline.C:
			t.Fatal("后台下载未结束")
		case <-ticker.C:
		}
	}
}

func pointUpdateFeed(t *testing.T, s *Server, latest string) {
	t.Helper()
	pointUpdateFeedWith(t, s, latest, nil)
}

func pointUpdateFeedWith(t *testing.T, s *Server, latest string, beforeDownload func(*http.Request)) {
	t.Helper()
	body := []byte(strings.Repeat("setup", 128))
	sum := sha256.Sum256(body)
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/release.json") {
			fmt.Fprintf(w, `{"version": %q, "publishedAt": "2026-09-18", "assets": [
			  {"os": %q, "arch": %q, "edition": %q, "kind": "installer", "name": "sniffy-setup.bin",
			   "url": "%s/sniffy-setup.bin", "size": %d, "sha256": %q}]}`,
				latest, runtime.GOOS, runtime.GOARCH, update.Edition, srv.URL, len(body), hex.EncodeToString(sum[:]))
			return
		}
		if beforeDownload != nil {
			beforeDownload(r)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	s.svc.SetUpdateChecker(&update.Checker{
		Feeds:   []string{srv.URL + "/release.json"},
		Current: "1.0.0",
		Client:  srv.Client(),
	})
	s.svc.SetUpdateDownloadDir(t.TempDir())
}

func TestUpdateStatusEndpoint(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t)

	rec := do(t, mux, http.MethodGet, "/api/update", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var state service.UpdateStateDTO
	decodeEnvelope(t, rec).into(t, &state)
	if state.Status != service.UpdateStatusIdle {
		t.Errorf("Status = %q,期望 %q", state.Status, service.UpdateStatusIdle)
	}
}

func TestUpdateCheckEndpointReportsNewVersion(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	pointUpdateFeed(t, s, "1.4.0")

	rec := do(t, mux, http.MethodPost, "/api/update/check", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var state service.UpdateStateDTO
	decodeEnvelope(t, rec).into(t, &state)
	if state.Latest != "1.4.0" || !state.Notify {
		t.Errorf("state = %+v,期望查到 1.4.0 并提醒", state)
	}
}

func TestUpdateCheckEndpointKeepsOKOnFeedFailure(t *testing.T) {
	t.Parallel()
	// newTestServer 默认就把清单源指向一个立刻拒绝连接的地址。
	_, mux := newTestServer(t)

	rec := do(t, mux, http.MethodPost, "/api/update/check", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,期望 200,响应 %s", rec.Code, rec.Body.String())
	}
	var state service.UpdateStateDTO
	decodeEnvelope(t, rec).into(t, &state)
	if state.Status != service.UpdateStatusError || state.Error == "" {
		t.Errorf("state = %+v,期望记录失败状态与原因", state)
	}
}

func TestUpdateSkipEndpoint(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	pointUpdateFeed(t, s, "1.4.0")
	do(t, mux, http.MethodPost, "/api/update/check", "")

	rec := do(t, mux, http.MethodPost, "/api/update/skip", `{"version":"1.4.0"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var state service.UpdateStateDTO
	decodeEnvelope(t, rec).into(t, &state)
	if state.SkippedVersion != "1.4.0" || state.Notify {
		t.Errorf("state = %+v,期望跳过 1.4.0 后不再提醒", state)
	}

	// skippedVersion 带 omitempty,须用新变量解码以免保留旧值。
	var restored service.UpdateStateDTO
	rec = do(t, mux, http.MethodPost, "/api/update/unskip", "")
	decodeEnvelope(t, rec).into(t, &restored)
	if restored.SkippedVersion != "" || !restored.Notify {
		t.Errorf("state = %+v,期望撤销跳过后恢复提醒", restored)
	}
}

func TestUpdateAutoCheckEndpoint(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t)

	rec := do(t, mux, http.MethodPost, "/api/update/auto", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var state service.UpdateStateDTO
	decodeEnvelope(t, rec).into(t, &state)
	if state.AutoCheck {
		t.Error("AutoCheck = true,关掉后状态应同步为 false")
	}

	if rec := do(t, mux, http.MethodPost, "/api/update/auto", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d,期望 400", rec.Code)
	}
}

func TestUpdateDownloadWithoutAssetConflicts(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t)

	rec := do(t, mux, http.MethodPost, "/api/update/download", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d,期望 409,响应 %s", rec.Code, rec.Body.String())
	}
}
