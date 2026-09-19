// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestDownloadHTTPFailuresLeaveNoPartialFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"源返回错误", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}},
		{"文件小于清单", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "小")
		}},
		{"传输中断", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "1024")
			fmt.Fprint(w, "半包")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asset, checker := newAssetServer(t, []byte("完整的安装包"))
			srv := httptest.NewTLSServer(tc.handler)
			defer srv.Close()
			asset.URL, checker.Client = srv.URL, srv.Client()
			dir := t.TempDir()
			if path, err := checker.Download(t.Context(), asset, dir, nil); err == nil || path != "" {
				t.Fatalf("下载失败仍返回安装包：%q, %v", path, err)
			}
			assertDirEmpty(t, dir)
		})
	}
}

func TestDownloadDestinationFailurePreservesExistingFiles(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"下载目录被文件占用", "安装包名称被目录占用"} {
		t.Run(scenario, func(t *testing.T) {
			asset, checker := newAssetServer(t, []byte("安装包"))
			dir := t.TempDir()
			blocked := filepath.Join(dir, "保留.txt")
			if scenario == "下载目录被文件占用" {
				dir = blocked
			} else {
				blocked = filepath.Join(dir, asset.Name, "保留.txt")
			}
			if err := os.MkdirAll(filepath.Dir(blocked), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(blocked, []byte("保留"), 0o600); err != nil {
				t.Fatal(err)
			}
			if path, err := checker.Download(t.Context(), asset, dir, nil); err == nil || path != "" {
				t.Fatalf("目标冲突应报告失败：%q, %v", path, err)
			}
			if body, err := os.ReadFile(blocked); err != nil || string(body) != "保留" {
				t.Fatalf("已有文件被覆盖：%q, %v", body, err)
			}
			if partial, err := filepath.Glob(filepath.Join(dir, "*.part")); err != nil || len(partial) != 0 {
				t.Fatalf("下载失败留下临时文件：%v, %v", partial, err)
			}
		})
	}
}

func TestDownloadFailurePreservesPreviousInstaller(t *testing.T) {
	t.Parallel()
	body := []byte("已校验的安装包")
	asset, checker := newAssetServer(t, body)
	dir := t.TempDir()
	path, err := checker.Download(t.Context(), asset, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	asset.SHA256 = strings.Repeat("00", 32)
	if _, err := checker.Download(t.Context(), asset, dir, nil); err == nil {
		t.Fatal("应拒绝校验不符的包")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
		t.Fatalf("下载失败损坏了原安装包：%q, %v", got, err)
	}
}

type updateReaderFunc func([]byte) (int, error)

func (f updateReaderFunc) Read(p []byte) (int, error) { return f(p) }

type updateWriterFunc func([]byte) (int, error)

func (f updateWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestDownloadProgressAndWriteFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := strings.NewReader(strings.Repeat("x", 512<<10))
		reader := updateReaderFunc(func(p []byte) (int, error) {
			time.Sleep(250 * time.Millisecond)
			return body.Read(p)
		})
		var progress []Progress
		written, _, err := copyWithProgress(io.Discard, reader, 512<<10, func(p Progress) { progress = append(progress, p) })
		if err != nil || written != 512<<10 || len(progress) == 0 {
			t.Fatalf("下载进度缺失：%d, %v, %v", written, progress, err)
		}
		var previous int64
		for _, p := range progress {
			if p.Done <= previous || p.Done > p.Total || p.Total != 512<<10 {
				t.Fatalf("进度异常：%+v", progress)
			}
			previous = p.Done
		}
	})
	diskFull := errors.New("磁盘空间不足")
	writer := updateWriterFunc(func([]byte) (int, error) { return 0, diskFull })
	_, _, err := copyWithProgress(writer, strings.NewReader("安装包"), 100, nil)
	if !errors.Is(err, diskFull) {
		t.Fatalf("写盘错误未保留：%v", err)
	}
}

func newAssetServer(t *testing.T, body []byte) (Asset, *Checker) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(body)
	asset := Asset{
		OS: "linux", Arch: "amd64", Edition: "headless", Kind: KindInstaller,
		Name:   "sniffy-installer.bin",
		URL:    srv.URL + "/sniffy-installer.bin",
		Size:   int64(len(body)),
		SHA256: hex.EncodeToString(sum[:]),
	}
	return asset, &Checker{Current: "1.0.0", Client: srv.Client()}
}

func TestDownloadWritesVerifiedFile(t *testing.T) {
	t.Parallel()
	body := []byte(strings.Repeat("sniffy", 5000))
	asset, checker := newAssetServer(t, body)
	dir := t.TempDir()

	var lastProgress Progress
	path, err := checker.Download(context.Background(), asset, dir, func(p Progress) { lastProgress = p })
	if err != nil {
		t.Fatalf("Download 失败: %v", err)
	}
	if got := filepath.Base(path); got != asset.Name {
		t.Errorf("落盘文件名 = %q,期望 %q", got, asset.Name)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取下载结果失败: %v", err)
	}
	if string(got) != string(body) {
		t.Error("下载内容与服务端返回不一致")
	}
	if lastProgress.Done != int64(len(body)) || lastProgress.Total != int64(len(body)) {
		t.Errorf("最终进度 = %+v,期望 %d/%d", lastProgress, len(body), len(body))
	}
}

func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()
	asset, checker := newAssetServer(t, []byte("real payload"))
	asset.SHA256 = strings.Repeat("ab", 32)
	dir := t.TempDir()

	if _, err := checker.Download(context.Background(), asset, dir, nil); err == nil {
		t.Fatal("Download 应在校验值不符时失败")
	}
	assertDirEmpty(t, dir)
}

func TestDownloadRejectsSizeMismatch(t *testing.T) {
	t.Parallel()
	asset, checker := newAssetServer(t, []byte("0123456789"))
	asset.Size = 4 // 清单登记的体积小于实际内容
	dir := t.TempDir()

	if _, err := checker.Download(context.Background(), asset, dir, nil); err == nil {
		t.Fatal("Download 应在体积与清单不符时失败")
	}
	assertDirEmpty(t, dir)
}

func TestDownloadCancelLeavesNoFile(t *testing.T) {
	t.Parallel()
	asset, checker := newAssetServer(t, []byte(strings.Repeat("x", 1<<20)))
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := checker.Download(ctx, asset, dir, nil); err == nil {
		t.Fatal("Download 应在 context 取消后失败")
	}
	assertDirEmpty(t, dir)
}

func TestDownloadRejectsUnsafeName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"../escape.bin", "sub/dir.bin", `..\escape.bin`, "", ".."} {
		asset := Asset{Kind: KindBinary, Name: name, URL: "https://cdn.example.com/x", Size: 5, SHA256: testSHA256}
		if _, err := (&Checker{}).Download(context.Background(), asset, dir, nil); err == nil {
			t.Errorf("Download 接受了不合法的文件名 %q", name)
		}
	}
	assertDirEmpty(t, dir)
}

func TestDownloadRejectsPlaintextURL(t *testing.T) {
	t.Parallel()
	asset := Asset{Kind: KindBinary, Name: "sniffy.bin", URL: "http://cdn.example.com/sniffy.bin", Size: 5, SHA256: testSHA256}
	if _, err := (&Checker{}).Download(context.Background(), asset, t.TempDir(), nil); err == nil {
		t.Fatal("Download 应拒绝 http 直链")
	}
}

func TestDownloadSetsExecutableBitForBinary(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不按权限位判定可执行")
	}
	for _, kind := range []string{KindBinary, KindInstaller} {
		asset, checker := newAssetServer(t, []byte("payload"))
		asset.Kind = kind
		path, err := checker.Download(context.Background(), asset, t.TempDir(), nil)
		if err != nil {
			t.Fatalf("%s: Download 失败: %v", kind, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: 读取文件信息失败: %v", kind, err)
		}
		perm := info.Mode().Perm()
		switch kind {
		case KindBinary:
			if perm != 0o755 {
				t.Errorf("binary 权限 = %v,期望 %v", perm, os.FileMode(0o755))
			}
		case KindInstaller:
			if perm&0o111 != 0 {
				t.Errorf("installer 权限 = %v,不该带执行位", perm)
			}
		}
	}
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("下载目录残留文件: %v", names)
	}
}
