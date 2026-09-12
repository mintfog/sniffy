// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux || darwin

package sysproxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type commandStep struct {
	args           []string
	stdout, stderr string
	exitCode       int
}

// PATH 仅包含替身目录，避免测试触及真实桌面设置；脚本只使用 POSIX shell 内建命令。
// 调用错误另存文件，确保 PointsTo 等返回布尔值的入口也能暴露参数不匹配。
func mockCommand(t *testing.T, name string, steps []commandStep) {
	t.Helper()

	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	failure := filepath.Join(dir, "failure")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	require.NoError(t, os.WriteFile(count, []byte("0\n"), 0600))
	var script strings.Builder
	fmt.Fprintf(&script, `#!/bin/sh
IFS= read -r n < %s
printf '%%s\n' "$((n+1))" > %s
fail() { printf '步骤 %%s: %%s\n' "$n" "$*" >> %s; exit 99; }
case "$n" in
`, quote(count), quote(count), quote(failure))
	for i, step := range steps {
		fmt.Fprintf(&script, "%d)\n[ \"$#\" -eq %d ] || fail '参数数量错误'\n", i, len(step.args))
		for _, arg := range step.args {
			fmt.Fprintf(&script, "[ \"$1\" = %s ] || fail \"参数错误: $1\"\nshift\n", quote(arg))
		}
		fmt.Fprintf(&script, "printf '%%s' %s\nprintf '%%s' %s >&2\nexit %d\n;;\n",
			quote(step.stdout), quote(step.stderr), step.exitCode)
	}
	script.WriteString("*) fail '非预期调用';;\nesac\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script.String()), 0700))
	t.Setenv("PATH", dir)
	t.Cleanup(func() {
		if out, err := os.ReadFile(failure); err == nil {
			t.Errorf("命令调用不符合预期: %s", out)
		} else if !os.IsNotExist(err) {
			t.Error(err)
		}
		out, err := os.ReadFile(count)
		require.NoError(t, err)
		require.Equal(t, strconv.Itoa(len(steps)), strings.TrimSpace(string(out)), "命令调用次数")
	})
}
