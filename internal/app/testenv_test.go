// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// 配置与缓存目录使用平台各自的环境变量，统一隔离以免读写用户数据。
func isolateAppDirs(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("APPDATA", filepath.Join(root, "config"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "cache"))
	t.Setenv(apiTokenEnv, "")
}

func writeAppFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("写入测试文件 %s: %v", path, err)
	}
}

// 子进程隔离全局参数与进程退出，避免影响同一测试二进制中的其他用例。
func appTestSubprocess(t *testing.T) ([]byte, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.timeout=25s")
	// testing 正常退出时按此标志写覆盖率，需指向父进程目录才能合并统计。
	if coverDir := flag.Lookup("test.gocoverdir"); testing.CoverMode() != "" && coverDir != nil && coverDir.Value.String() != "" {
		cmd.Args = append(cmd.Args, "-test.gocoverdir="+coverDir.Value.String())
	}
	cmd.Env = append(os.Environ(), "SNIFFY_APP_TEST_CHILD="+t.Name())
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("测试子进程超时: %v\n%s", ctx.Err(), out)
	}
	return out, err
}

func inAppTestSubprocess(t *testing.T) bool {
	t.Helper()
	return os.Getenv("SNIFFY_APP_TEST_CHILD") == t.Name()
}
