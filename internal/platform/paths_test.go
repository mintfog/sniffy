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
//(Windows 上 ConfigDir 是会被漫游配置文件同步的 %AppData%)。
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
