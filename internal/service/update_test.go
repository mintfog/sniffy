// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/update"
)

func TestUpdateConfigBroadcastsUpdatePreferences(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		patch   map[string]any
		auto    bool
		skipped string
	}{
		{"自动检查", map[string]any{"updateCheck": false}, false, ""},
		{"跳过版本", map[string]any{"updateSkipped": "2.0.0"}, true, "2.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			events, cancel := svc.Bus().Subscribe()
			defer cancel()
			before := svc.UpdateState()

			svc.UpdateConfig(tc.patch)

			state := svc.UpdateState()
			if state.AutoCheck != tc.auto || state.SkippedVersion != tc.skipped || state.Revision != before.Revision+1 {
				t.Fatalf("更新快照 = %+v,期望 autoCheck=%t、skippedVersion=%q、revision=%d", state, tc.auto, tc.skipped, before.Revision+1)
			}
			select {
			case event := <-events:
				if event.Type != core.EventUpdateState || event.Payload != state {
					t.Fatalf("更新事件 = %+v,期望 update_state 携带 %+v", event, state)
				}
			default:
				t.Fatal("更新配置后未广播 update_state")
			}

			svc.UpdateConfig(tc.patch)
			svc.UpdateConfig(map[string]any{"recording": false})
			if got := svc.UpdateState(); got != state {
				t.Fatalf("更新偏好未变时快照发生变化：%+v,期望 %+v", got, state)
			}
			select {
			case event := <-events:
				t.Fatalf("更新偏好未变时收到多余事件：%+v", event)
			default:
			}
		})
	}
}

type updateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f updateRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckForUpdateTimeout(t *testing.T) {
	transport := updateRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	for _, tc := range []struct {
		name          string
		callerTimeout time.Duration
		wantDuration  time.Duration
	}{
		{"总超时", time.Minute, 30 * time.Second},
		{"调用方提前超时", 5 * time.Second, 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				svc := newTestService(t)
				svc.SetUpdateChecker(&update.Checker{
					Current: "1.0.0",
					Feeds: []string{
						"https://updates.example/primary.json",
						"https://updates.example/backup1.json",
						"https://updates.example/backup2.json",
						"https://updates.example/backup3.json",
					},
					Client: &http.Client{Transport: transport},
				})
				ctx, cancel := context.WithTimeout(t.Context(), tc.callerTimeout)
				defer cancel()

				started := time.Now()
				state, err := svc.CheckForUpdate(ctx)

				if !errors.Is(err, context.DeadlineExceeded) || state.Status != UpdateStatusError || state.ErrorStage != "check" {
					t.Fatalf("超时结果 = %+v,错误 = %v,期望检查失败并保留超时错误", state, err)
				}
				if elapsed := time.Since(started); elapsed != tc.wantDuration {
					t.Fatalf("检查耗时 = %v,期望 %v", elapsed, tc.wantDuration)
				}
			})
		})
	}
}

func TestUpdateDownloadFailureCanRetry(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	var requests atomic.Int32
	newUpdateFeedWith(t, svc, "1.3.0", update.KindInstaller, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(installerBody)
	})
	if _, _, err := svc.DownloadedUpdate(); err == nil {
		t.Fatal("尚未下载时不应有安装包")
	}
	if _, err := svc.CheckForUpdate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartUpdateDownload(); err != nil {
		t.Fatal(err)
	}
	failed := waitUpdateStatus(t, svc, UpdateStatusError)
	if failed.Error == "" || failed.ErrorStage != "download" || failed.DownloadedPath != "" {
		t.Fatalf("失败快照不正确：%+v", failed)
	}
	if _, err := svc.StartUpdateDownload(); err != nil {
		t.Fatal(err)
	}
	done := waitUpdateStatus(t, svc, UpdateStatusDownloaded)
	if done.Error != "" || done.ErrorStage != "" || done.DownloadedPath == "" || requests.Load() != 2 {
		t.Fatalf("重试未完成：%+v，请求数 %d", done, requests.Load())
	}
}

func TestUpdateBinaryDownloadRevealsFile(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	newUpdateFeedWith(t, svc, "1.3.0", update.KindBinary, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(installerBody)
	})
	state, err := svc.CheckForUpdate(t.Context())
	if err != nil || state.InstallAction != update.ActionReveal {
		t.Fatalf("免安装程序的检查结果：%+v, %v", state, err)
	}
	if _, err := svc.StartUpdateDownload(); err != nil {
		t.Fatal(err)
	}
	state = waitUpdateStatus(t, svc, UpdateStatusDownloaded)
	path, action, err := svc.DownloadedUpdate()
	if err != nil || path != state.DownloadedPath || action != update.ActionReveal || state.InstallAction != action {
		t.Fatalf("免安装程序下载结果：路径 %q,动作 %q,状态 %+v,错误 %v", path, action, state, err)
	}
}

func TestUpdateRecheckRetainsOnlyCurrentInstaller(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	path := downloadedInstallerPath(t, svc, "1.3.0")
	state, err := svc.CheckForUpdate(t.Context())
	if err != nil || state.Status != UpdateStatusDownloaded || state.DownloadedPath != path {
		t.Fatalf("同版本重新检查丢失安装包：%+v, %v", state, err)
	}
	newUpdateFeed(t, svc, "1.4.0")
	state, err = svc.CheckForUpdate(t.Context())
	if err != nil || state.Status != UpdateStatusAvailable || state.DownloadedPath != "" || state.Downloaded != 0 {
		t.Fatalf("新版本沿用了旧安装包：%+v, %v", state, err)
	}
}

func TestUpdateDownloadUsesUserDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DOWNLOAD_DIR", dir)
	svc := newTestService(t)
	newUpdateFeed(t, svc, "1.3.0")
	svc.SetUpdateDownloadDir("")
	if _, err := svc.CheckForUpdate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartUpdateDownload(); err != nil {
		t.Fatal(err)
	}
	state := waitUpdateStatus(t, svc, UpdateStatusDownloaded)
	if filepath.Dir(state.DownloadedPath) != dir {
		t.Fatalf("安装包未落到用户下载目录：%s", state.DownloadedPath)
	}
}

func TestUpdatePreferencesSurviveRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := New(nil, nil, dir, t.TempDir())
	svc.SetUpdateAutoCheck(false)
	svc.SkipUpdateVersion("1.3.0")
	reopened := New(nil, nil, dir, t.TempDir())
	state := reopened.UpdateState()
	if state.AutoCheck || state.SkippedVersion != "1.3.0" {
		t.Fatalf("更新偏好未保存：%+v", state)
	}
}

var installerBody = []byte(strings.Repeat("sniffy-setup", 512))

func newUpdateFeed(t *testing.T, svc *Service, latest string) *httptest.Server {
	t.Helper()
	return newUpdateFeedWith(t, svc, latest, update.KindInstaller, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(installerBody)
	})
}

// 清单校验要求产物地址为 HTTPS,测试服务使用 TLS。
func newUpdateFeedWith(t *testing.T, svc *Service, latest, kind string, serveInstaller http.HandlerFunc) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(installerBody)
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/release.json") {
			fmt.Fprintf(w, `{
			  "version": %q,
			  "publishedAt": "2026-09-18",
			  "notesUrl": "https://gosniffy.com/docs/changelog/",
			  "assets": [
			    {"os": %q, "arch": %q, "edition": %q, "kind": %q, "name": "sniffy-setup.bin",
			     "url": "%s/sniffy-setup.bin", "size": %d, "sha256": %q}
			  ]
			}`, latest, runtime.GOOS, runtime.GOARCH, update.Edition, kind, srv.URL, len(installerBody), hex.EncodeToString(sum[:]))
			return
		}
		serveInstaller(w, r)
	}))
	t.Cleanup(srv.Close)
	svc.SetUpdateChecker(&update.Checker{
		Feeds:   []string{srv.URL + "/release.json"},
		Current: "1.0.0",
		Client:  srv.Client(),
	})
	svc.SetUpdateDownloadDir(t.TempDir())
	return srv
}

func downloadedInstallerPath(t *testing.T, svc *Service, latest string) string {
	t.Helper()
	newUpdateFeed(t, svc, latest)
	if _, err := svc.CheckForUpdate(t.Context()); err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}
	if _, err := svc.StartUpdateDownload(); err != nil {
		t.Fatalf("StartUpdateDownload 失败: %v", err)
	}
	return waitUpdateStatus(t, svc, UpdateStatusDownloaded).DownloadedPath
}

func TestUpdateStateStartsIdle(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	state := svc.UpdateState()
	if state.Status != UpdateStatusIdle {
		t.Errorf("Status = %q,期望 %q", state.Status, UpdateStatusIdle)
	}
	if state.Notify {
		t.Error("Notify = true,还没查过就不该提醒")
	}
	if !state.AutoCheck {
		t.Error("AutoCheck = false,自动检查默认应开启")
	}
}

func TestCheckForUpdateFindsNewVersion(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	newUpdateFeed(t, svc, "1.3.0")

	state, err := svc.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}
	if state.Status != UpdateStatusAvailable {
		t.Errorf("Status = %q,期望 %q", state.Status, UpdateStatusAvailable)
	}
	if state.Latest != "1.3.0" {
		t.Errorf("Latest = %q,期望 1.3.0", state.Latest)
	}
	if !state.Notify {
		t.Error("Notify = false,有未跳过的新版本时应提醒")
	}
	if state.Asset == nil || state.Asset.Name != "sniffy-setup.bin" {
		t.Errorf("Asset = %+v,期望带上当前平台的产物", state.Asset)
	}
	if state.CheckedAt == "" {
		t.Error("CheckedAt 为空,应记录本次检查时间")
	}
}

func TestCheckForUpdateSameVersion(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	newUpdateFeed(t, svc, "1.0.0")

	state, err := svc.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}
	if state.Status != UpdateStatusLatest {
		t.Errorf("Status = %q,期望 %q", state.Status, UpdateStatusLatest)
	}
	if state.Notify {
		t.Error("Notify = true,已是最新时不该提醒")
	}
}

func TestCheckForUpdateRecordsFailure(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.SetUpdateChecker(&update.Checker{
		Feeds:   []string{"http://127.0.0.1:1/release.json"},
		Current: "1.0.0",
	})

	state, err := svc.CheckForUpdate(context.Background())
	if err == nil {
		t.Fatal("CheckForUpdate 应在清单源不可达时返回错误")
	}
	if state.Status != UpdateStatusError {
		t.Errorf("Status = %q,期望 %q", state.Status, UpdateStatusError)
	}
	if state.Error == "" {
		t.Error("Error 为空,失败原因应留在状态里供界面展示")
	}
	if state.ErrorStage != "check" {
		t.Errorf("ErrorStage = %q,期望 check", state.ErrorStage)
	}
	if state.Notify {
		t.Error("Notify = true,查询失败时没有可提醒的新版本")
	}
}

func TestSkipUpdateVersionSilencesOnlyThatVersion(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	newUpdateFeed(t, svc, "1.3.0")
	if _, err := svc.CheckForUpdate(context.Background()); err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}

	state := svc.SkipUpdateVersion("")
	if state.SkippedVersion != "1.3.0" {
		t.Errorf("SkippedVersion = %q,期望 1.3.0(不带参数即跳过当前最新版)", state.SkippedVersion)
	}
	if state.Notify {
		t.Error("Notify = true,已跳过的版本不该继续提醒")
	}
	if state.Status != UpdateStatusAvailable {
		t.Errorf("Status = %q,跳过只影响提醒,不改变「有新版可下载」的事实", state.Status)
	}

	newUpdateFeed(t, svc, "1.4.0")
	next, err := svc.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}
	if !next.Notify {
		t.Error("Notify = false,比被跳过的版本更新的版本应重新提醒")
	}

	restored := svc.ClearSkippedUpdateVersion()
	if restored.SkippedVersion != "" {
		t.Errorf("SkippedVersion = %q,撤销后应为空", restored.SkippedVersion)
	}
}

func TestStartUpdateDownloadWritesInstaller(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	newUpdateFeed(t, svc, "1.3.0")
	if _, err := svc.CheckForUpdate(context.Background()); err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}

	if _, err := svc.StartUpdateDownload(); err != nil {
		t.Fatalf("StartUpdateDownload 失败: %v", err)
	}
	state := waitUpdateStatus(t, svc, UpdateStatusDownloaded)
	if filepath.Base(state.DownloadedPath) != "sniffy-setup.bin" {
		t.Errorf("DownloadedPath = %q,期望以产物文件名结尾", state.DownloadedPath)
	}
	got, err := os.ReadFile(state.DownloadedPath)
	if err != nil {
		t.Fatalf("读取下载结果失败: %v", err)
	}
	if string(got) != string(installerBody) {
		t.Error("落盘内容与服务端返回不一致")
	}
}

func TestCheckForUpdateKeepsDownloadedInstaller(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	path := downloadedInstallerPath(t, svc, "1.3.0")

	svc.SetUpdateChecker(&update.Checker{
		Feeds:   []string{"http://127.0.0.1:1/release.json"},
		Current: "1.0.0",
	})
	state, err := svc.CheckForUpdate(t.Context())
	if err == nil {
		t.Fatal("CheckForUpdate 应在清单源不可达时返回错误")
	}
	if state.Status != UpdateStatusDownloaded {
		t.Errorf("Status = %q,期望 %q:安装包还在,安装入口就不该消失", state.Status, UpdateStatusDownloaded)
	}
	if state.DownloadedPath != path {
		t.Errorf("DownloadedPath = %q,期望保留 %q", state.DownloadedPath, path)
	}
	if state.Error == "" {
		t.Error("Error 为空,查询失败的原因仍要告诉用户")
	}
}

func TestCheckForUpdateDropsMissingInstaller(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	path := downloadedInstallerPath(t, svc, "1.3.0")
	if err := os.Remove(path); err != nil {
		t.Fatalf("删除安装包失败: %v", err)
	}

	state, err := svc.CheckForUpdate(t.Context())
	if err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}
	if state.Status != UpdateStatusAvailable {
		t.Errorf("Status = %q,期望 %q", state.Status, UpdateStatusAvailable)
	}
	if state.DownloadedPath != "" || state.Downloaded != 0 || state.Total != 0 {
		t.Errorf("下载记录 = %q %d/%d,期望清空", state.DownloadedPath, state.Downloaded, state.Total)
	}
}

func TestDownloadedUpdateDropsMissingFile(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	path := downloadedInstallerPath(t, svc, "1.3.0")

	got, action, err := svc.DownloadedUpdate()
	if err != nil || got != path || action != svc.UpdateState().InstallAction {
		t.Fatalf("DownloadedUpdate = %q, %q, %v,期望路径 %q 且安装动作与快照一致", got, action, err, path)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("删除安装包失败: %v", err)
	}
	if _, _, err := svc.DownloadedUpdate(); err == nil {
		t.Fatal("DownloadedUpdate 应在安装包不在原处时返回错误")
	}
	state := svc.UpdateState()
	if state.Status != UpdateStatusAvailable || state.DownloadedPath != "" {
		t.Errorf("状态 = %q(路径 %q),期望退回 %q 且清空路径", state.Status, state.DownloadedPath, UpdateStatusAvailable)
	}
}

func TestCancelUpdateDownloadReturnsSettledState(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	streaming := make(chan struct{})
	newUpdateFeedWith(t, svc, "1.3.0", update.KindInstaller, func(w http.ResponseWriter, r *http.Request) {
		// 只发一半就挂住,让下载停在进行中,直到客户端取消。
		_, _ = w.Write(installerBody[:len(installerBody)/2])
		w.(http.Flusher).Flush()
		close(streaming)
		select {
		case <-r.Context().Done():
		case <-t.Context().Done():
		}
	})
	if _, err := svc.CheckForUpdate(t.Context()); err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}
	if _, err := svc.StartUpdateDownload(); err != nil {
		t.Fatalf("StartUpdateDownload 失败: %v", err)
	}
	select {
	case <-streaming:
	case <-time.After(5 * time.Second):
		t.Fatal("等待下载开始超时")
	}
	before := svc.UpdateState()
	duplicate, err := svc.StartUpdateDownload()
	if err != nil || duplicate.Status != UpdateStatusDownloading || duplicate.Revision != before.Revision {
		t.Fatalf("重复下载改变了正在进行的任务：%+v, %v", duplicate, err)
	}
	checking, err := svc.CheckForUpdate(t.Context())
	if err != nil || checking.Status != UpdateStatusDownloading {
		t.Fatalf("下载中检查更新覆盖了下载状态：%+v, %v", checking, err)
	}

	state := svc.CancelUpdateDownload()
	if state.Status != UpdateStatusAvailable {
		t.Fatalf("取消后返回的状态 = %q(错误 %q),期望 %q", state.Status, state.Error, UpdateStatusAvailable)
	}
	if state.Downloaded != 0 || state.Total != 0 {
		t.Errorf("取消后进度 = %d/%d,期望清零", state.Downloaded, state.Total)
	}
}

func TestStartUpdateDownloadWithoutAsset(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if _, err := svc.StartUpdateDownload(); err == nil {
		t.Fatal("StartUpdateDownload 应在没有匹配产物时返回错误")
	}
}

func TestStartUpdateDownloadDuringCheck(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	newUpdateFeed(t, svc, "1.3.0")
	if _, err := svc.CheckForUpdate(t.Context()); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			fmt.Fprint(w, `{"version":"1.4.0","assets":[]}`)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	svc.SetUpdateChecker(&update.Checker{Feeds: []string{srv.URL}, Current: "1.0.0", Client: srv.Client()})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	checked := make(chan error, 1)
	go func() {
		_, err := svc.CheckForUpdate(ctx)
		checked <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("等待检查请求超时")
	}

	state, err := svc.StartUpdateDownload()
	if err == nil || state.Status != UpdateStatusChecking {
		t.Fatalf("检查期间应拒绝下载旧产物,状态 = %q,错误 = %v", state.Status, err)
	}
	release <- struct{}{}
	select {
	case err := <-checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("等待检查完成超时")
	}
	if state := svc.UpdateState(); state.Latest != "1.4.0" || state.Status != UpdateStatusAvailable {
		t.Fatalf("检查完成后的状态 = %+v", state)
	}
}

func TestUpdateStateBroadcast(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	newUpdateFeed(t, svc, "1.3.0")
	events, cancel := svc.Bus().Subscribe()
	defer cancel()

	if _, err := svc.CheckForUpdate(context.Background()); err != nil {
		t.Fatalf("CheckForUpdate 失败: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Type != core.EventUpdateState {
				continue
			}
			state, ok := e.Payload.(UpdateStateDTO)
			if !ok {
				t.Fatalf("事件载荷类型 = %T,期望 UpdateStateDTO", e.Payload)
			}
			if state.Status == UpdateStatusAvailable && state.Latest == "1.3.0" {
				return
			}
		case <-deadline:
			t.Fatal("等待 update_state 事件超时")
		}
	}
}

func TestShouldAutoCheckUpdate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.SetUpdateChecker(&update.Checker{Current: "1.0.0"})
	if !svc.ShouldAutoCheckUpdate() {
		t.Error("ShouldAutoCheckUpdate = false,默认应开启")
	}

	if state := svc.SetUpdateAutoCheck(false); state.AutoCheck {
		t.Error("AutoCheck = true,关掉后状态快照应同步为 false")
	}
	if svc.ShouldAutoCheckUpdate() {
		t.Error("ShouldAutoCheckUpdate = true,用户关掉后不该再自动查询")
	}

	svc.SetUpdateAutoCheck(true)
	svc.SetUpdateChecker(&update.Checker{Current: "0.0.0-dev"})
	if svc.ShouldAutoCheckUpdate() {
		t.Error("ShouldAutoCheckUpdate = true,开发构建每次都会「发现新版」,不该自动查询")
	}
}

func TestUpdateRevisionAdvancesOnEveryChange(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	newUpdateFeed(t, svc, "1.3.0")

	last := svc.UpdateState().Revision
	steps := []struct {
		name string
		do   func() UpdateStateDTO
	}{
		{"检查更新", func() UpdateStateDTO {
			state, err := svc.CheckForUpdate(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			return state
		}},
		{"跳过版本", func() UpdateStateDTO { return svc.SkipUpdateVersion("") }},
		{"撤销跳过", svc.ClearSkippedUpdateVersion},
		{"关闭自动检查", func() UpdateStateDTO { return svc.SetUpdateAutoCheck(false) }},
		{"下载并等待完成", func() UpdateStateDTO {
			if _, err := svc.StartUpdateDownload(); err != nil {
				t.Fatal(err)
			}
			return waitUpdateStatus(t, svc, UpdateStatusDownloaded)
		}},
	}
	for _, step := range steps {
		got := step.do().Revision
		if got <= last {
			t.Errorf("%s后 Revision = %d,未超过之前的 %d", step.name, got, last)
		}
		last = got
	}

	if got := svc.CancelUpdateDownload().Revision; got != last {
		t.Errorf("空操作后 Revision 从 %d 变成 %d", last, got)
	}
}

func waitUpdateStatus(t *testing.T, svc *Service, want string) UpdateStateDTO {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := svc.UpdateState()
		if state.Status == want {
			return state
		}
		if state.Status == UpdateStatusError {
			t.Fatalf("状态进入 error: %s", state.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待状态 %q 超时,当前 %q", want, svc.UpdateState().Status)
	return UpdateStateDTO{}
}
