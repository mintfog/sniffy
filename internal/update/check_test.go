// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package update

import (
	"context"
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
	"time"
)

func TestCheckFallbackAfterInvalidManifest(t *testing.T) {
	t.Parallel()
	for _, invalid := range []string{`{`, `{"version":"latest"}`, `{"version":"2.0.0","assets":[{"name":"sniffy"}]}`} {
		var srv *httptest.Server
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/primary" {
				fmt.Fprint(w, invalid)
				return
			}
			fmt.Fprint(w, manifestJSON(srv.URL, "2.0.0"))
		}
		t.Run(invalid, func(t *testing.T) {
			var checker *Checker
			srv, checker = newFeedServer(t, handler)
			checker.Feeds = []string{srv.URL + "/primary", srv.URL + "/fallback"}
			result, err := checker.Check(t.Context())
			if err != nil || !result.Available || result.Feed != checker.Feeds[1] {
				t.Fatalf("主源清单不可用时未回退：%+v, %v", result, err)
			}
		})
	}
}

func TestCheckCancellationStopsFallback(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	var fallback atomic.Int32
	srv, checker := newFeedServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/primary" {
			close(started)
			<-r.Context().Done()
			return
		}
		fallback.Add(1)
	})
	checker.Feeds = []string{srv.URL + "/primary", srv.URL + "/fallback"}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := checker.Check(ctx)
		result <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("清单请求没有发出")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrNoManifest) {
			t.Fatalf("取消错误未保留：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后检查未结束")
	}
	if fallback.Load() != 0 {
		t.Fatal("取消后仍请求了备用源")
	}
}

func TestCheckUsesConfiguredFeedAndRequestHeaders(t *testing.T) {
	var srv *httptest.Server
	srv, checker := newFeedServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("清单请求头不正确：%v", r.Header)
		}
		if !strings.HasPrefix(r.UserAgent(), "sniffy/0.0.0-dev ") {
			t.Errorf("开发构建 User-Agent = %q", r.UserAgent())
		}
		fmt.Fprint(w, manifestJSON(srv.URL, "2.0.0"))
	})
	t.Setenv(FeedEnv, srv.URL)
	checker.Current = ""
	result, err := checker.Check(t.Context())
	if err != nil || result.Feed != srv.URL || !result.Dev {
		t.Fatalf("环境变量指定清单未生效：%+v, %v", result, err)
	}
	if got := NewChecker("1.0.0").Feeds; len(got) != 1 || got[0] != srv.URL {
		t.Fatalf("检查器未继承分发源：%v", got)
	}
}

// 清单校验要求产物地址为 HTTPS,测试服务使用 TLS。
func newFeedServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Checker) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	return srv, &Checker{Current: "1.0.0", Client: srv.Client()}
}

// testSHA256 是格式合法的占位摘要,供不涉及下载的清单用例使用。
var testSHA256 = strings.Repeat("ab", 32)

func manifestJSON(base, version string) string {
	return fmt.Sprintf(`{
	  "version": %q,
	  "publishedAt": "2026-09-18",
	  "notesUrl": "https://gosniffy.com/docs/changelog/",
	  "assets": [
	    {"os": %q, "arch": %q, "edition": %q, "kind": "binary", "name": "sniffy-test", "url": "%s/sniffy-test", "size": 5, "sha256": %q}
	  ]
	}`, version, runtime.GOOS, runtime.GOARCH, Edition, base, testSHA256)
}

func TestCheckReportsNewerVersion(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	srv, checker := newFeedServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, manifestJSON(srv.URL, "1.2.0"))
	})
	checker.Feeds = []string{srv.URL + "/release.json"}

	res, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if !res.Available {
		t.Error("Available = false,期望识别出新版本")
	}
	if res.Manifest.Version != "1.2.0" {
		t.Errorf("Version = %q,期望 1.2.0", res.Manifest.Version)
	}
	if !res.HasAsset {
		t.Fatalf("没有匹配当前平台(%s/%s, %s)的产物", runtime.GOOS, runtime.GOARCH, Edition)
	}
	if res.Feed != checker.Feeds[0] {
		t.Errorf("Feed = %q,期望 %q", res.Feed, checker.Feeds[0])
	}
}

func TestCheckSameVersionIsNotAvailable(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	srv, checker := newFeedServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, manifestJSON(srv.URL, "1.0.0"))
	})
	checker.Feeds = []string{srv.URL + "/release.json"}

	res, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if res.Available {
		t.Error("Available = true,同版本不该提示更新")
	}
}

func TestCheckDevBuildSkipsComparison(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	srv, checker := newFeedServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, manifestJSON(srv.URL, "9.9.9"))
	})
	checker.Feeds = []string{srv.URL + "/release.json"}
	checker.Current = "0.0.0-dev"

	res, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if !res.Dev {
		t.Error("Dev = false,期望识别为开发构建")
	}
	if res.Available {
		t.Error("Available = true,开发构建不该被判成落后于线上版本")
	}
}

func TestCheckFallsBackToNextFeed(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	srv, checker := newFeedServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/primary.json") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, manifestJSON(srv.URL, "1.5.0"))
	})
	checker.Feeds = []string{srv.URL + "/primary.json", srv.URL + "/fallback.json"}

	res, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if res.Feed != checker.Feeds[1] {
		t.Errorf("Feed = %q,期望回退到 %q", res.Feed, checker.Feeds[1])
	}
	if res.Manifest.Version != "1.5.0" {
		t.Errorf("Version = %q,期望 1.5.0", res.Manifest.Version)
	}
}

func TestCheckAllFeedsFailed(t *testing.T) {
	t.Parallel()
	srv, checker := newFeedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	checker.Feeds = []string{srv.URL + "/a.json", srv.URL + "/b.json"}

	if _, err := checker.Check(context.Background()); err == nil {
		t.Fatal("Check 应在所有源都失败时返回错误")
	} else if !strings.Contains(err.Error(), "a.json") || !strings.Contains(err.Error(), "b.json") {
		t.Errorf("错误信息应包含每个失败的源,实际: %v", err)
	}
}

func TestValidateRejectsPlaintextAsset(t *testing.T) {
	t.Parallel()
	m := Manifest{
		Version: "1.2.0",
		Assets: []Asset{{
			OS: "linux", Arch: "amd64", Edition: "headless", Kind: KindBinary, Name: "sniffy",
			URL: "http://cdn.example.com/sniffy", Size: 5, SHA256: testSHA256,
		}},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("Validate 应拒绝 http 直链")
	}
}

func TestValidateRequiresKindSizeAndChecksum(t *testing.T) {
	t.Parallel()
	valid := Asset{
		OS: "linux", Arch: "amd64", Edition: "headless", Kind: KindBinary, Name: "sniffy",
		URL: "https://cdn.example.com/sniffy", Size: 5, SHA256: testSHA256,
	}
	if err := (Manifest{Version: "1.2.0", Assets: []Asset{valid}}).Validate(); err != nil {
		t.Fatalf("Validate 拒绝了合法清单: %v", err)
	}

	cases := map[string]func(*Asset){
		"缺少类型":         func(a *Asset) { a.Kind = "" },
		"类型未知":         func(a *Asset) { a.Kind = "portable" },
		"缺少体积":         func(a *Asset) { a.Size = 0 },
		"体积为负":         func(a *Asset) { a.Size = -1 },
		"缺少 SHA256":    func(a *Asset) { a.SHA256 = "" },
		"SHA256 过短":    func(a *Asset) { a.SHA256 = testSHA256[:62] },
		"SHA256 非十六进制": func(a *Asset) { a.SHA256 = strings.Repeat("zz", 32) },
	}
	for name, mutate := range cases {
		a := valid
		mutate(&a)
		if err := (Manifest{Version: "1.2.0", Assets: []Asset{a}}).Validate(); err == nil {
			t.Errorf("%s: Validate 应拒绝该产物", name)
		}
	}
}

func TestValidateRejectsUnparseableVersion(t *testing.T) {
	t.Parallel()
	if err := (Manifest{Version: "latest"}).Validate(); err == nil {
		t.Fatal("Validate 应拒绝无法解析的版本号")
	}
	if err := (Manifest{}).Validate(); err == nil {
		t.Fatal("Validate 应拒绝缺少版本号的清单")
	}
}

func TestAssetForMatchesPlatformAndEdition(t *testing.T) {
	t.Parallel()
	m := Manifest{
		Version: "1.2.0",
		Assets: []Asset{
			{OS: "darwin", Arch: archUniversal, Edition: "desktop", Name: "sniffy.dmg", URL: "https://cdn/sniffy.dmg"},
			{OS: "linux", Arch: "amd64", Edition: "headless", Name: "sniffy", URL: "https://cdn/sniffy"},
			{OS: "linux", Arch: "amd64", Edition: "desktop", Name: "sniffy.AppImage", URL: "https://cdn/sniffy.AppImage"},
		},
	}
	cases := []struct {
		goos, goarch, edition string
		want                  string
	}{
		{"darwin", "arm64", "desktop", "sniffy.dmg"},
		{"darwin", "amd64", "desktop", "sniffy.dmg"},
		{"linux", "amd64", "headless", "sniffy"},
		{"linux", "amd64", "desktop", "sniffy.AppImage"},
	}
	for _, c := range cases {
		a, ok := m.AssetFor(c.goos, c.goarch, c.edition)
		if !ok {
			t.Errorf("AssetFor(%s, %s, %s) 没有匹配到产物", c.goos, c.goarch, c.edition)
			continue
		}
		if a.Name != c.want {
			t.Errorf("AssetFor(%s, %s, %s) = %q,期望 %q", c.goos, c.goarch, c.edition, a.Name, c.want)
		}
	}
	if _, ok := m.AssetFor("windows", "amd64", "desktop"); ok {
		t.Error("AssetFor 对清单里没有的平台不该返回产物")
	}
}

func TestAppArchiveIsSelectedSeparately(t *testing.T) {
	t.Parallel()
	m := Manifest{
		Version: "1.2.0",
		Assets: []Asset{
			{OS: "darwin", Arch: "arm64", Edition: "desktop", Kind: KindBinary, Name: "sniffy-desktop-darwin-arm64.app.tar.gz"},
			{OS: "darwin", Arch: "arm64", Edition: "desktop", Kind: KindInstaller, Name: "sniffy-desktop-darwin-arm64-installer.dmg"},
		},
	}
	if a, _ := m.AssetFor("darwin", "arm64", "desktop"); a.Kind != KindInstaller {
		t.Errorf("AssetFor 选中了 %q,应用归档不能作为默认产物", a.Name)
	}
	if a, ok := m.AppArchiveFor("darwin", "arm64", "desktop"); !ok || !a.IsAppArchive() {
		t.Errorf("AppArchiveFor = %q, %v,期望选中应用归档", a.Name, ok)
	}
	if _, ok := m.AppArchiveFor("darwin", "amd64", "desktop"); ok {
		t.Error("AppArchiveFor 不该跨架构匹配")
	}
}

// 修改包级 appReplaceable,不能并行。
func TestCheckPrefersAppArchiveWhenReplaceable(t *testing.T) {
	var srv *httptest.Server
	srv, checker := newFeedServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"version": "1.2.0", "assets": [
		  {"os": %[1]q, "arch": %[2]q, "edition": %[3]q, "kind": "installer", "name": "sniffy-setup", "url": "%[4]s/setup", "size": 5, "sha256": %[5]q},
		  {"os": %[1]q, "arch": %[2]q, "edition": %[3]q, "kind": "binary", "name": "sniffy.app.tar.gz", "url": "%[4]s/app", "size": 5, "sha256": %[5]q}
		]}`, runtime.GOOS, runtime.GOARCH, Edition, srv.URL, testSHA256)
	})
	checker.Feeds = []string{srv.URL + "/release.json"}
	original := appReplaceable
	t.Cleanup(func() { appReplaceable = original })

	for _, tc := range []struct {
		replaceable bool
		want        string
	}{
		{false, "sniffy-setup"},
		{true, "sniffy.app.tar.gz"},
	} {
		appReplaceable = func() bool { return tc.replaceable }
		res, err := checker.Check(t.Context())
		if err != nil {
			t.Fatalf("Check 失败: %v", err)
		}
		if !res.HasAsset || res.Asset.Name != tc.want {
			t.Errorf("可原地替换=%v 时选中 %q,期望 %q", tc.replaceable, res.Asset.Name, tc.want)
		}
	}

	checker.SkipAppArchive = true
	if res, err := checker.Check(t.Context()); err != nil || res.Asset.Name != "sniffy-setup" {
		t.Errorf("SkipAppArchive 时选中 %q (%v),期望安装介质", res.Asset.Name, err)
	}
}

func TestAssetInstallAction(t *testing.T) {
	t.Parallel()
	wantInstaller := ActionReveal
	if Edition == "desktop" {
		switch runtime.GOOS {
		case "windows":
			wantInstaller = ActionRun
		case "darwin":
			wantInstaller = ActionOpen
		}
	}
	for _, tc := range []struct {
		kind string
		want string
	}{
		{KindInstaller, wantInstaller},
		{KindBinary, ActionReveal},
		{"", ActionReveal},
	} {
		if got := (Asset{Kind: tc.kind, Name: "sniffy"}).InstallAction(); got != tc.want {
			t.Errorf("%s/%s 的 %q 产物安装动作 = %q,期望 %q", runtime.GOOS, Edition, tc.kind, got, tc.want)
		}
	}
}

func TestAppArchiveInstallAction(t *testing.T) {
	t.Parallel()
	want := ActionReveal
	if Edition == "desktop" && runtime.GOOS == "darwin" {
		want = ActionReplace
	}
	if got := (Asset{Kind: KindBinary, Name: "sniffy" + AppArchiveSuffix}).InstallAction(); got != want {
		t.Errorf("%s/%s 的应用归档安装动作 = %q,期望 %q", runtime.GOOS, Edition, got, want)
	}
	if got := (Asset{Kind: KindInstaller, Name: "sniffy" + AppArchiveSuffix}).InstallAction(); got != installerAction {
		t.Errorf("安装介质不应按文件名当作应用归档,得到 %q", got)
	}
}

func TestParseFeedListKeepsOnlyHTTPS(t *testing.T) {
	t.Parallel()
	got := parseFeedList(" https://a.example/release.json , ftp://b.example/x , not-a-url, http://c.example/r.json, https://d.example/r.json ")
	want := []string{"https://a.example/release.json", "https://d.example/r.json"}
	if len(got) != len(want) {
		t.Fatalf("parseFeedList = %v,期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseFeedList[%d] = %q,期望 %q", i, got[i], want[i])
		}
	}
}

func TestAppBundleOfRequiresExecutableOwnBundle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ exe, want string }{
		{"/Applications/Sniffy.app/Contents/MacOS/sniffy", "/Applications/Sniffy.app"},
		{"/Users/u/My Tools.app/Contents/MacOS/sniffy", "/Users/u/My Tools.app"},
		{"/Users/u/Tools.app/Sniffy.app/Contents/MacOS/sniffy", ""},
		{"/Applications/Sniffy.app/Contents/Resources/sniffy", ""},
		{"/usr/local/bin/sniffy", ""},
	} {
		exe, want := filepath.FromSlash(tc.exe), filepath.FromSlash(tc.want)
		if got := appBundleOf(exe); got != want {
			t.Errorf("appBundleOf(%q) = %q,期望 %q", exe, got, want)
		}
	}
}

func TestBackupInTheWay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundle := filepath.Join(dir, "Sniffy.app")
	if BackupInTheWay(bundle) {
		t.Fatal("没有 .bak 时不应视为占用")
	}
	if err := os.Mkdir(bundle+".bak", 0o700); err != nil {
		t.Fatal(err)
	}
	if !BackupInTheWay(bundle) {
		t.Error("已有 .bak 目录时应视为占用")
	}
	// 悬空符号链接同样会被替换器删除。
	dangling := filepath.Join(dir, "Other.app")
	if err := os.Symlink(filepath.Join(dir, "missing"), dangling+".bak"); err != nil {
		t.Skip("无法创建符号链接:", err)
	}
	if !BackupInTheWay(dangling) {
		t.Error("悬空的 .bak 符号链接应视为占用")
	}
}
