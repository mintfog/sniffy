// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package sysproxy

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func darwinListStep() commandStep {
	return commandStep{
		args: []string{"-listallnetworkservices"},
		stdout: "An asterisk (*) denotes that a network service is disabled.\n" +
			"Wi-Fi\n*Bluetooth PAN\n办公网络's $(name); USB\n",
	}
}

func darwinWriteSteps(clear bool) []commandStep {
	steps := []commandStep{darwinListStep()}
	for _, svc := range []string{"Wi-Fi", "办公网络's $(name); USB"} {
		if clear {
			steps = append(steps,
				commandStep{args: []string{"-setwebproxystate", svc, "off"}},
				commandStep{args: []string{"-setsecurewebproxystate", svc, "off"}},
			)
		} else {
			steps = append(steps,
				commandStep{args: []string{"-setwebproxy", svc, "::1", "3128"}},
				commandStep{args: []string{"-setsecurewebproxy", svc, "::1", "3128"}},
				commandStep{args: []string{"-setproxybypassdomains", svc, "localhost", "127.0.0.1", "::1", "*.local", "169.254/16"}},
			)
		}
	}
	return steps
}

func TestProxyWrites(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(fmt.Sprintf("清除=%v", clear), func(t *testing.T) {
			invoke := func() error {
				if clear {
					return Clear()
				}
				return Set("::1", 3128)
			}
			t.Run("全部成功", func(t *testing.T) {
				mockCommand(t, "networksetup", darwinWriteSteps(clear))
				require.NoError(t, invoke())
			})
			for i := 1; i < len(darwinWriteSteps(clear)); i++ {
				t.Run(fmt.Sprintf("步骤%d失败后继续", i), func(t *testing.T) {
					steps := darwinWriteSteps(clear)
					steps[i].exitCode = 5
					steps[i].stdout = "写入详情"
					steps[i].stderr = "权限不足"
					mockCommand(t, "networksetup", steps)
					err := invoke()
					var exitErr *exec.ExitError
					require.ErrorAs(t, err, &exitErr)
					assert.Equal(t, 5, exitErr.ExitCode())
					assert.Contains(t, err.Error(), strings.Join(steps[i].args, " "))
					assert.Contains(t, err.Error(), "写入详情")
					assert.Contains(t, err.Error(), "权限不足")
				})
			}
			t.Run("聚合多个错误", func(t *testing.T) {
				steps := darwinWriteSteps(clear)
				for i := 1; i < len(steps); i++ {
					steps[i].exitCode = i
					steps[i].stderr = fmt.Sprintf("失败%d", i)
				}
				mockCommand(t, "networksetup", steps)
				err := invoke()
				require.Error(t, err)
				joined, ok := err.(interface{ Unwrap() []error })
				require.True(t, ok, "聚合错误应保留每个子错误")
				require.Len(t, joined.Unwrap(), len(steps)-1)
				for i, child := range joined.Unwrap() {
					var exitErr *exec.ExitError
					require.ErrorAs(t, child, &exitErr)
					assert.Equal(t, i+1, exitErr.ExitCode())
					assert.Contains(t, child.Error(), fmt.Sprintf("失败%d", i+1))
				}
			})
		})
	}
}

func TestUnavailableServices(t *testing.T) {
	for _, tt := range []struct {
		name, output string
		exitCode     int
	}{
		{"列举失败", "说明\nWi-Fi\n", 4},
		{"空输出", "", 0},
		{"仅表头", "说明\n", 0},
		{"全部禁用", "说明\n*Wi-Fi\n*Ethernet\n", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, op := range []string{"Set", "Clear", "PointsTo"} {
				t.Run(op, func(t *testing.T) {
					mockCommand(t, "networksetup", []commandStep{
						{
							args:     []string{"-listallnetworkservices"},
							stdout:   tt.output,
							exitCode: tt.exitCode,
						},
					})
					if op == "PointsTo" {
						assert.False(t, PointsTo("localhost", 8080))
						return
					}

					var err error
					if op == "Set" {
						err = Set("localhost", 8080)
					} else {
						err = Clear()
					}
					require.Error(t, err)
					if tt.exitCode != 0 {
						var exitErr *exec.ExitError
						require.ErrorAs(t, err, &exitErr)
						assert.Equal(t, tt.exitCode, exitErr.ExitCode())
						assert.Contains(t, err.Error(), "列举网络服务失败")
					} else {
						assert.EqualError(t, err, "未找到可用网络服务")
					}
				})
			}
		})
	}
}

func TestMissingNetworksetup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	assert.True(t, errors.Is(Set("localhost", 8080), exec.ErrNotFound))
	assert.True(t, errors.Is(Clear(), exec.ErrNotFound))
	assert.False(t, PointsTo("localhost", 8080))
}

func TestPointsTo(t *testing.T) {
	const match = "Enabled: Yes\nServer: ::1\nPort: 3128\n"
	for _, tt := range []struct {
		name, first, second string
		firstExit           int
		reads               int
		want                bool
	}{
		{"首个服务匹配即停止", match, "", 0, 1, true},
		{"后续服务匹配", "Enabled: No\nServer: ::1\nPort: 3128", match, 0, 2, true},
		{"读取失败后继续", match, match, 2, 2, true},
		{"已禁用", "Enabled: No\nServer: ::1\nPort: 3128", "", 0, 2, false},
		{"主机不匹配", "Enabled: Yes\nServer: other\nPort: 3128", "", 0, 2, false},
		{"端口不匹配", "Enabled: Yes\nServer: ::1\nPort: 8080", "", 0, 2, false},
		{"畸形输出", "garbage", "Port: 3128", 0, 2, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			steps := []commandStep{
				darwinListStep(),
				{args: []string{"-getwebproxy", "Wi-Fi"}, stdout: tt.first, exitCode: tt.firstExit},
				{args: []string{"-getwebproxy", "办公网络's $(name); USB"}, stdout: tt.second},
			}[:tt.reads+1]
			mockCommand(t, "networksetup", steps)
			assert.Equal(t, tt.want, PointsTo("::1", 3128))
		})
	}
}
