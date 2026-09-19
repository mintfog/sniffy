// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCertificatesDir(t *testing.T) {
	base := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", base)
	case "darwin":
		t.Setenv("HOME", base)
	default:
		t.Setenv("XDG_CONFIG_HOME", base)
	}

	configBase, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("获取测试配置目录失败: %v", err)
	}
	want := filepath.Join(configBase, appDirName, "certificates")
	got, err := CertificatesDir()
	if err != nil {
		t.Fatalf("创建证书目录失败: %v", err)
	}
	if got != want {
		t.Fatalf("证书目录不正确: want %q, got %q", want, got)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("证书目录不存在: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("证书路径不是目录: %s", got)
	}
}

func TestConfigDirPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不使用 Unix 权限位")
	}
	base := t.TempDir()
	if runtime.GOOS == "darwin" {
		t.Setenv("HOME", base)
	} else {
		t.Setenv("XDG_CONFIG_HOME", base)
	}
	configBase, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(configBase, appDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigDir(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("配置目录权限 = %o, want 700", got)
	}
}

// TestCacheDir 锁定缓存目录落在系统缓存位置,且不在 ConfigDir 之下
// (Windows 上 ConfigDir 是会被漫游配置文件同步的 %AppData%)。
func TestCacheDir(t *testing.T) {
	base := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", base)
		t.Setenv("AppData", filepath.Join(base, "roaming"))
	case "darwin":
		t.Setenv("HOME", base)
	default:
		t.Setenv("XDG_CACHE_HOME", base)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	}

	cacheBase, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("获取测试缓存目录失败: %v", err)
	}
	want := filepath.Join(cacheBase, appDirName)
	got, err := CacheDir()
	if err != nil {
		t.Fatalf("创建缓存目录失败: %v", err)
	}
	if got != want {
		t.Fatalf("缓存目录不正确: want %q, got %q", want, got)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("缓存目录不存在: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("缓存路径不是目录: %s", got)
	}

	cfg, err := ConfigDir()
	if err != nil {
		t.Fatalf("获取配置目录失败: %v", err)
	}
	if strings.HasPrefix(got, cfg+string(filepath.Separator)) {
		t.Fatalf("缓存目录不应落在配置目录内: cache=%q config=%q", got, cfg)
	}
}

func TestDownloadsDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "cache"))
	downloads := filepath.Join(root, "Downloads")
	custom := filepath.Join(root, "下载")
	for _, dir := range []string{downloads, custom} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "普通文件")
	if err := os.WriteFile(file, []byte("保留"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		specified string
		want      string
	}{
		{"指定下载目录", custom, custom},
		{"用户下载目录", "", downloads},
		{"指定路径不是目录", file, downloads},
		{"指定目录已移走", filepath.Join(root, "不存在"), downloads},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_DOWNLOAD_DIR", tc.specified)
			got, err := DownloadsDir()
			if err != nil || got != tc.want {
				t.Fatalf("DownloadsDir() = %q, %v，期望 %q", got, err, tc.want)
			}
			if info, err := os.Stat(got); err != nil || !info.IsDir() {
				t.Fatalf("下载目录未创建：%v", err)
			}
		})
	}
}

func TestDownloadsDirFallsBackToCache(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "cache"))
	t.Setenv("XDG_DOWNLOAD_DIR", "")
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cache, appDirName, "updates")
	got, err := DownloadsDir()
	if err != nil || got != want {
		t.Fatalf("DownloadsDir() = %q, %v，期望 %q", got, err, want)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("下载目录未创建：%v", err)
	}
}

func TestDownloadsDirReportsCreationFailure(t *testing.T) {
	for _, blocked := range []string{"缓存目录", "更新目录"} {
		t.Run(blocked, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("USERPROFILE", root)
			t.Setenv("XDG_DOWNLOAD_DIR", "")
			t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
			t.Setenv("LOCALAPPDATA", filepath.Join(root, "cache"))
			cache, err := os.UserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(cache, appDirName)
			if blocked == "更新目录" {
				path = filepath.Join(path, "updates")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("保留"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := DownloadsDir(); err == nil || got != "" {
				t.Fatalf("DownloadsDir() = %q, %v，期望目录创建失败", got, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "保留" {
				t.Fatalf("已有文件被改动：%q, %v", data, err)
			}
		})
	}
}

func TestDownloadsDirWithoutHome(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_DOWNLOAD_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	got, err := DownloadsDir()
	want := filepath.Join(".sniffy-fallback", appDirName, "updates")
	if err != nil || got != want {
		t.Fatalf("DownloadsDir() = %q, %v，期望 %q", got, err, want)
	}
}
