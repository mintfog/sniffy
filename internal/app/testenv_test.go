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
	"strings"
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
	cmd := appTestCommand(t)
	return cmd.CombinedOutput()
}

func appTestCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.timeout=25s")
	if coverDir := flag.Lookup("test.gocoverdir"); testing.CoverMode() != "" && coverDir != nil && coverDir.Value.String() != "" {
		// Windows 上并发发布同名覆盖率元数据会导致重命名失败。
		childDir := t.TempDir()
		cmd.Args = append(cmd.Args, "-test.gocoverdir="+childDir)
		t.Cleanup(func() { collectAppTestCoverage(t, childDir, coverDir.Value.String()) })
	}
	cmd.Env = append(os.Environ(), "SNIFFY_APP_TEST_CHILD="+t.Name())
	return cmd
}

func collectAppTestCoverage(t *testing.T, childDir, parentDir string) {
	t.Helper()
	entries, err := os.ReadDir(childDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		// 父测试会生成同一二进制的元数据；计数文件名包含子进程的 PID 和时间戳。
		if !strings.HasPrefix(entry.Name(), "covcounters.") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(childDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parentDir, entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func inAppTestSubprocess(t *testing.T) bool {
	t.Helper()
	return os.Getenv("SNIFFY_APP_TEST_CHILD") == t.Name()
}
