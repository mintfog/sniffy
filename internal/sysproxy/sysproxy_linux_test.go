// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package sysproxy

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func linuxSetSteps(host, port string) []commandStep {
	return []commandStep{
		{args: []string{"set", "org.gnome.system.proxy.http", "host", host}},
		{args: []string{"set", "org.gnome.system.proxy.http", "port", port}},
		{args: []string{"set", "org.gnome.system.proxy.https", "host", host}},
		{args: []string{"set", "org.gnome.system.proxy.https", "port", port}},
		{args: []string{"set", "org.gnome.system.proxy", "use-same-proxy", "true"}},
		{args: []string{"set", "org.gnome.system.proxy", "ignore-hosts", "['localhost', '127.0.0.0/8', '::1']"}},
		{args: []string{"set", "org.gnome.system.proxy", "mode", "manual"}},
	}
}

func TestSet(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "proxy's $(name); example"} {
		t.Run(host, func(t *testing.T) {
			mockCommand(t, "gsettings", linuxSetSteps(host, "3128"))
			require.NoError(t, Set(host, 3128))
		})
	}
}

func TestSetStopsOnFailure(t *testing.T) {
	for i := range linuxSetSteps("localhost", "8080") {
		t.Run(fmt.Sprintf("步骤%d", i+1), func(t *testing.T) {
			steps := linuxSetSteps("localhost", "8080")[:i+1]
			steps[i].stdout = "写入失败详情"
			steps[i].stderr = "权限不足"
			steps[i].exitCode = 7
			mockCommand(t, "gsettings", steps)

			err := Set("localhost", 8080)
			require.Error(t, err)
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, 7, exitErr.ExitCode())
			assert.Contains(t, err.Error(), fmt.Sprint(steps[i].args))
			assert.Contains(t, err.Error(), "写入失败详情")
			assert.Contains(t, err.Error(), "权限不足")
		})
	}
}

func TestClear(t *testing.T) {
	for _, code := range []int{0, 3} {
		t.Run(fmt.Sprintf("退出码%d", code), func(t *testing.T) {
			mockCommand(t, "gsettings", []commandStep{
				{
					args:     []string{"set", "org.gnome.system.proxy", "mode", "none"},
					stderr:   "写入失败",
					exitCode: code,
				},
			})

			err := Clear()
			if code == 0 {
				require.NoError(t, err)
				return
			}
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, code, exitErr.ExitCode())
			assert.Contains(t, err.Error(), "写入失败")
		})
	}
}

func TestMissingGsettings(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	assert.ErrorIs(t, Set("localhost", 8080), errNoGsettings)
	assert.ErrorIs(t, Clear(), errNoGsettings)
	assert.False(t, PointsTo("localhost", 8080))
}

func TestPointsTo(t *testing.T) {
	for _, tt := range []struct {
		name, mode, host, port string
		failAt                 int
		reads                  int
		want                   bool
	}{
		{"匹配", "'manual'\n", "'localhost'\n", "8080\n", -1, 3, true},
		{"空白", " \t'manual'\r\n", " 'localhost' \n", " 8080\r\n", -1, 3, true},
		{"直连", "'none'", "", "", -1, 1, false},
		{"自动模式", "'auto'", "", "", -1, 1, false},
		{"模式读取失败", "'manual'", "", "", 0, 1, false},
		{"主机读取失败", "'manual'", "'localhost'", "", 1, 2, false},
		{"主机不匹配", "'manual'", "'other'", "", -1, 2, false},
		{"端口读取失败", "'manual'", "'localhost'", "8080", 2, 3, false},
		{"端口不匹配", "'manual'", "'localhost'", "8081", -1, 3, false},
		{"端口畸形", "'manual'", "'localhost'", "invalid", -1, 3, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			steps := []commandStep{
				{args: []string{"get", "org.gnome.system.proxy", "mode"}, stdout: tt.mode},
				{args: []string{"get", "org.gnome.system.proxy.http", "host"}, stdout: tt.host},
				{args: []string{"get", "org.gnome.system.proxy.http", "port"}, stdout: tt.port},
			}[:tt.reads]
			if tt.failAt >= 0 {
				steps[tt.failAt].exitCode = 2
			}
			mockCommand(t, "gsettings", steps)
			assert.Equal(t, tt.want, PointsTo("localhost", 8080))
		})
	}
}

func TestPointsToIPv6(t *testing.T) {
	mockCommand(t, "gsettings", []commandStep{
		{args: []string{"get", "org.gnome.system.proxy", "mode"}, stdout: "'manual'"},
		{args: []string{"get", "org.gnome.system.proxy.http", "host"}, stdout: "'::1'"},
		{args: []string{"get", "org.gnome.system.proxy.http", "port"}, stdout: "65535"},
	})
	assert.True(t, PointsTo("::1", 65535))
}
