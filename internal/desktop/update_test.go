// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/update"
)

func TestBridgeUpdatePreferencesAndMissingInstaller(t *testing.T) {
	b := newTestBridge()
	if state := b.GetUpdateState(); state.Status != service.UpdateStatusIdle {
		t.Fatalf("初始状态：%+v", state)
	}
	if _, err := b.InstallUpdate(); err == nil {
		t.Fatal("尚未下载时应拒绝安装")
	}
	if b.RevealUpdateDownload() {
		t.Fatal("尚未下载时不应打开文件夹")
	}
	if _, err := b.DownloadUpdate(); err == nil {
		t.Fatal("尚未查到产物时应拒绝下载")
	}
	if state := b.SetUpdateAutoCheck(false); state.AutoCheck {
		t.Fatal("关闭自动检查未生效")
	}
	if state := b.SkipUpdateVersion("2.0.0"); state.SkippedVersion != "2.0.0" {
		t.Fatalf("跳过版本未生效：%+v", state)
	}
	if state := b.ClearSkippedUpdateVersion(); state.SkippedVersion != "" {
		t.Fatalf("恢复提醒未生效：%+v", state)
	}
	before := b.GetUpdateState()
	if after := b.CancelUpdateDownload(); after.Revision != before.Revision {
		t.Fatal("空取消不应改变状态")
	}
}

func TestBridgeUpdateDownloadAndMissingFileRecovery(t *testing.T) {
	b := newTestBridge()
	body := []byte("测试安装包")
	sum := sha256.Sum256(body)
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release.json" {
			fmt.Fprintf(w, `{"version":"2.0.0","assets":[{"os":%q,"arch":%q,"edition":"desktop","kind":"installer","name":"sniffy.bin","url":%q,"size":%d,"sha256":"%x"}]}`, runtime.GOOS, runtime.GOARCH, server.URL+"/sniffy.bin", len(body), sum)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	b.app.Service.SetUpdateChecker(&update.Checker{Current: "1.0.0", Feeds: []string{server.URL + "/release.json"}, Client: server.Client()})
	b.app.Service.SetUpdateDownloadDir(t.TempDir())
	t.Cleanup(func() { b.CancelUpdateDownload() })
	if state := b.CheckUpdate(); state.Status != service.UpdateStatusAvailable || !state.Notify {
		t.Fatalf("检查结果：%+v", state)
	}
	if _, err := b.DownloadUpdate(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for b.GetUpdateState().Status == service.UpdateStatusDownloading && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	state := b.GetUpdateState()
	if state.Status != service.UpdateStatusDownloaded {
		t.Fatalf("下载未完成：%+v", state)
	}
	if got, err := os.ReadFile(state.DownloadedPath); err != nil || string(got) != string(body) {
		t.Fatalf("下载内容异常：%q, %v", got, err)
	}
	if err := os.Remove(state.DownloadedPath); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.InstallUpdate(); ok || err == nil {
		t.Fatalf("文件缺失应拒绝安装：%v, %v", ok, err)
	}
	if state := b.GetUpdateState(); state.Status != service.UpdateStatusAvailable || state.DownloadedPath != "" {
		t.Fatalf("缺失安装包未恢复下载入口：%+v", state)
	}
}
