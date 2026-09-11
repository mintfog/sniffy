// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mintfog/sniffy/internal/platform"
)

func TestAPITokenPublicLifecycle(t *testing.T) {
	isolateAppDirs(t)
	t.Setenv(apiTokenEnv, " \t\n")
	if got := LoadAPIToken(); got != "" {
		t.Fatal("首次启动应尚无 token")
	}
	token, rotated, err := EnsureTokenSecrecy()
	if err != nil || token != "" || rotated {
		t.Fatalf("凭据缺失时安全检查 = (%q, %v, %v)", token, rotated, err)
	}
	token, path, err := EnsureAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		t.Fatal("生成的 token 应为 32 字节随机数的十六进制编码")
	}
	dir, err := platform.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, apiTokenFileName) {
		t.Fatalf("token 路径 = %q", path)
	}
	if got := LoadAPIToken(); got != token {
		t.Fatal("公开读取接口与生成结果不一致")
	}
	again, rotated, err := EnsureTokenSecrecy()
	if err != nil || rotated || again != token {
		t.Fatalf("安全凭据未被复用: rotated=%v, err=%v", rotated, err)
	}
	t.Setenv(apiTokenEnv, "  environment-token\n")
	if got := LoadAPIToken(); got != "environment-token" {
		t.Fatal("环境变量应去空白后覆盖文件值")
	}
	if got, path, err := EnsureAPIToken(); err != nil || got != "environment-token" || path != "" {
		t.Fatalf("环境变量覆盖失败: path=%q, err=%v", path, err)
	}
	if got := loadAPITokenFile(dir); got != token {
		t.Fatal("环境变量覆盖期间磁盘凭据发生变化")
	}
}

func TestEnsureAPITokenRejectsInvalidExistingFile(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		directory  bool
	}{
		{"空文件", "", false},
		{"空白文件", " \n\t", false},
		{"目录占位", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, apiTokenFileName)
			if tt.directory {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeAppFixture(t, path, tt.data)
			}
			token, returnedPath, err := ensureAPITokenFile(dir)
			if err == nil || token != "" || returnedPath != "" {
				t.Fatalf("无效凭据应拒绝发布: token长度=%d, path=%q, err=%v", len(token), returnedPath, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != apiTokenFileName {
				t.Fatalf("失败后存在临时文件: %v", entries)
			}
			if !tt.directory {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != tt.data {
					t.Fatalf("失败后原文件变化: %q, %v", data, err)
				}
			}
		})
	}
}

func TestTokenDirLockReleasesAfterCallbackFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wantErr := errors.New("持久化失败")
	if err := withTokenDirLock(dir, func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("回调错误未传递: %v", err)
	}
	called := false
	if err := withTokenDirLock(dir, func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("失败后无法再次获得锁: %v", err)
	}
}

func TestEnsureAPITokenLockFailure(t *testing.T) {
	isolateAppDirs(t)
	dir, err := platform.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "api_token.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	token, path, err := EnsureAPIToken()
	if err == nil || token != "" || path != "" {
		t.Fatalf("锁不可用时应拒绝发布: path=%q, err=%v", path, err)
	}
	if _, err := os.Stat(filepath.Join(dir, apiTokenFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("锁不可用时仍创建了凭据: %v", err)
	}
}

func TestAPITokenUnavailableConfigDir(t *testing.T) {
	isolateAppDirs(t)
	dir, err := platform.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	writeAppFixture(t, dir, "占用配置目录路径")
	if got := LoadAPIToken(); got != "" {
		t.Fatal("配置目录不可用时读取到了凭据")
	}
	token, path, err := EnsureAPIToken()
	if err == nil || token != "" || path != "" {
		t.Fatalf("配置目录不可用时未拒绝创建凭据: path=%q, err=%v", path, err)
	}
	if token, rotated, err := EnsureTokenSecrecy(); token != "" || rotated || err != nil {
		t.Fatalf("配置目录不可用时应跳过轮换: rotated=%v, err=%v", rotated, err)
	}
}
