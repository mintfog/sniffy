// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func makeTokenDirReadOnly(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 目录写权限场景")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	probe, err := os.CreateTemp(dir, "probe-*")
	if err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		t.Skip("当前用户可绕过目录写权限")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("目录权限故障不符合预期: %v", err)
	}
}

func assertNoTempTokens(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("失败后残留临时文件: %s", entry.Name())
		}
	}
}

func TestPublishTokenReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	makeTokenDirReadOnly(t, dir)
	token, returnedPath, err := ensureAPITokenFile(dir)
	if !errors.Is(err, os.ErrPermission) || token != "" || returnedPath != "" {
		t.Fatalf("发布失败未返回文件权限错误: path=%q err=%v", returnedPath, err)
	}
	if _, err := os.Stat(filepath.Join(dir, apiTokenFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("失败后仍有 token 文件: %v", err)
	}
	assertNoTempTokens(t, dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	token, _, err = ensureAPITokenFile(dir)
	if err != nil || len(token) != 64 || loadAPITokenFile(dir) != token {
		t.Fatalf("目录恢复后发布失败: %v", err)
	}
}

func TestRotateTokenReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, apiTokenFileName)
	writeAppFixture(t, path, "exposed-token\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	// 提前创建锁文件，使故障发生在凭据写入阶段。
	if err := withTokenDirLock(dir, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	makeTokenDirReadOnly(t, dir)
	token, rotated, err := ensureTokenSecrecy(dir)
	if !errors.Is(err, os.ErrPermission) || token != "" || rotated {
		t.Fatalf("轮换失败未拒绝凭据: rotated=%v err=%v", rotated, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "exposed-token\n" {
		t.Fatalf("失败后旧凭据被修改: %q %v", data, err)
	}
	wide, err := filePermTooWide(path)
	if err != nil || !wide {
		t.Fatalf("失败后旧凭据权限变化: wide=%v err=%v", wide, err)
	}
	assertNoTempTokens(t, dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	token, rotated, err = ensureTokenSecrecy(dir)
	if err != nil || !rotated || len(token) != 64 || loadAPITokenFile(dir) != token {
		t.Fatalf("目录恢复后轮换失败: rotated=%v err=%v", rotated, err)
	}
}

func TestRotateTokenRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 权限位轮换场景")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, apiTokenFileName)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAppFixture(t, filepath.Join(path, "marker"), "preserved")
	err := withTokenDirLock(dir, func() error {
		token, rotated, err := rotateIfWide(dir, path)
		if token != "" || rotated {
			t.Error("重命名失败仍返回了新凭据")
		}
		return err
	})
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) || !strings.Contains(err.Error(), "轮换 token 文件") {
		t.Fatalf("未返回重命名错误: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(path, "marker"))
	if err != nil || string(data) != "preserved" {
		t.Fatalf("轮换失败破坏了目标目录: %q %v", data, err)
	}
	assertNoTempTokens(t, dir)
}
