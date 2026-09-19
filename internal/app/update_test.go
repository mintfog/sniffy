// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/update"
)

type updateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f updateRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateCheckScheduleAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := newUpdateTestApp(t, "1.0.0")
		var requests atomic.Int32
		a.Service.SetUpdateChecker(&update.Checker{
			Current: "1.0.0",
			Feeds:   []string{"https://updates.example/release.json"},
			Client: &http.Client{Transport: updateRoundTripFunc(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"version":"2.0.0","assets":[]}`)),
				}, nil
			})},
		})
		a.StartUpdateCheck()
		synctest.Wait()
		if requests.Load() != 0 {
			t.Fatal("启动时未等待首次检查间隔")
		}
		time.Sleep(updateFirstDelay)
		synctest.Wait()
		if requests.Load() != 1 || !a.Service.UpdateState().Notify {
			t.Fatalf("首次检查未提示新版本：请求数 %d", requests.Load())
		}
		a.Service.SetUpdateAutoCheck(false)
		time.Sleep(updateInterval)
		synctest.Wait()
		if requests.Load() != 1 {
			t.Fatal("关闭自动检查后仍发送请求")
		}
		a.Service.SetUpdateAutoCheck(true)
		time.Sleep(updateInterval)
		synctest.Wait()
		if requests.Load() != 2 {
			t.Fatalf("重新开启后没有继续周期检查：%d", requests.Load())
		}
		a.stopUpdateCheck()
		synctest.Wait()
		time.Sleep(updateInterval)
		if requests.Load() != 2 {
			t.Fatal("退出后仍然检查更新")
		}
	})
}

func newUpdateTestApp(t *testing.T, current string) *App {
	t.Helper()
	svc := service.New(nil, core.NewEventBus(), t.TempDir(), t.TempDir())
	// 用本地拒绝连接地址隔离线上清单源。
	svc.SetUpdateChecker(&update.Checker{
		Feeds:   []string{"http://127.0.0.1:1/release.json"},
		Current: current,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &App{Service: svc, Logger: NewLogger(false), updateCtx: ctx, updateCancel: cancel}
}

func holdUntilDone(t *testing.T, r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-t.Context().Done():
	}
}

func TestAutoCheckSkippedWhenDisabled(t *testing.T) {
	t.Parallel()
	a := newUpdateTestApp(t, "1.0.0")
	a.Service.SetUpdateAutoCheck(false)

	a.checkUpdateOnce()

	if got := a.Service.UpdateState().Status; got != service.UpdateStatusIdle {
		t.Errorf("Status = %q,期望保持 %q(未发起查询)", got, service.UpdateStatusIdle)
	}
}

func TestAutoCheckSkippedForDevBuild(t *testing.T) {
	t.Parallel()
	a := newUpdateTestApp(t, "0.0.0-dev")

	a.checkUpdateOnce()

	if got := a.Service.UpdateState().Status; got != service.UpdateStatusIdle {
		t.Errorf("Status = %q,期望保持 %q(未发起查询)", got, service.UpdateStatusIdle)
	}
}

func TestAutoCheckRecordsFailure(t *testing.T) {
	t.Parallel()
	a := newUpdateTestApp(t, "1.0.0")

	a.checkUpdateOnce()

	if got := a.Service.UpdateState().Status; got != service.UpdateStatusError {
		t.Errorf("Status = %q,期望 %q", got, service.UpdateStatusError)
	}
}

func TestStopCancelsInFlightCheck(t *testing.T) {
	t.Parallel()
	a := newUpdateTestApp(t, "1.0.0")
	requested := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		holdUntilDone(t, r)
	}))
	t.Cleanup(srv.Close)
	a.Service.SetUpdateChecker(&update.Checker{Feeds: []string{srv.URL}, Current: "1.0.0", Client: srv.Client()})

	checked := make(chan struct{})
	go func() {
		a.checkUpdateOnce()
		close(checked)
	}()
	awaitAppSignal(t, requested, "检查请求发出")
	a.stopUpdateCheck()
	select {
	case <-checked:
	case <-time.After(2 * time.Second):
		t.Fatal("关停后进行中的检查没有随之结束")
	}
}

func TestStopRemovesPartialUpdateDownload(t *testing.T) {
	if !appScenarioProcess(t) {
		return
	}
	body := []byte(strings.Repeat("sniffy-setup", 512))
	sum := sha256.Sum256(body)
	streaming := make(chan struct{})
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/release.json") {
			fmt.Fprintf(w, `{"version": "9.9.9", "assets": [
			  {"os": %q, "arch": %q, "edition": %q, "kind": "installer", "name": "sniffy-setup.bin",
			   "url": "%s/sniffy-setup.bin", "size": %d, "sha256": %q}]}`,
				runtime.GOOS, runtime.GOARCH, update.Edition, srv.URL, len(body), hex.EncodeToString(sum[:]))
			return
		}
		// 只发一半就挂住,让退出发生在下载途中。
		_, _ = w.Write(body[:len(body)/2])
		w.(http.Flusher).Flush()
		close(streaming)
		holdUntilDone(t, r)
	}))
	t.Cleanup(srv.Close)

	a := buildRunningApp(t)
	dir := t.TempDir()
	a.Service.SetUpdateChecker(&update.Checker{Feeds: []string{srv.URL + "/release.json"}, Current: "1.0.0", Client: srv.Client()})
	a.Service.SetUpdateDownloadDir(dir)
	if _, err := a.Service.CheckForUpdate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Service.StartUpdateDownload(); err != nil {
		t.Fatal(err)
	}
	awaitAppSignal(t, streaming, "安装包开始传输")

	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("退出后下载目录残留 %s", e.Name())
	}
}
