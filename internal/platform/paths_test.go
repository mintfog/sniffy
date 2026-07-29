// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package platform

import (
	"os"
	"path/filepath"
	"runtime"
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
