// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package truststore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// `security default-keychain -d user` 输出的路径带前导空白与引号包裹;
// 解析失败必须返回空串,调用方据此回退到标准钥匙串位置。
func TestParseDefaultKeychain(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"典型输出", `    "/Users/foo/Library/Keychains/login.keychain-db"` + "\n", "/Users/foo/Library/Keychains/login.keychain-db"},
		{"无引号", "/Users/foo/Library/Keychains/login.keychain", "/Users/foo/Library/Keychains/login.keychain"},
		{"空输出", "", ""},
		{"仅空白", "  \n\t", ""},
		{"非绝对路径", `"login.keychain-db"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDefaultKeychain(tt.in); got != tt.want {
				t.Fatalf("parseDefaultKeychain(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// 校验对 security 输出中稳定锚点(-128/user canceled/trust settings)的识别,
// 以及分支优先级:用户取消 > 信任设置失败 > 超时;未识别输出保留原文以免丢失线索。
func TestInterpretSecurityErr(t *testing.T) {
	exitErr := errors.New("exit status 1")
	tests := []struct {
		name   string
		runErr error
		out    string
		want   string
	}{
		{"错误码 -128 视为取消", exitErr, "The authorization was canceled by the user. (-128)", "truststore:canceled"},
		{"user canceled 大小写不敏感", exitErr, "User Canceled", "truststore:canceled"},
		{"英式拼写 cancelled", exitErr, "operation user cancelled", "truststore:canceled"},
		{"取消优先于信任设置失败", exitErr, "SecTrustSettingsSetTrustSettings: canceled (-128)", "truststore:canceled"},
		{"信任设置失败携带原文", exitErr, "SecTrustSettingsSetTrustSettings: unknown error", "truststore:trust_settings:SecTrustSettingsSetTrustSettings: unknown error"},
		{"trust settings 锚点", exitErr, "could not change trust settings", "truststore:trust_settings:could not change trust settings"},
		{"超时", context.DeadlineExceeded, "", "truststore:timeout:90"},
		{"取消优先于超时", context.DeadlineExceeded, "user canceled", "truststore:canceled"},
		{"未识别输出保留原文并去首尾空白", exitErr, "  some weird failure \n", "some weird failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpretSecurityErr(tt.runErr, []byte(tt.out))
			if got == nil || got.Error() != tt.want {
				t.Fatalf("interpretSecurityErr(%v, %q) = %v, want %q", tt.runErr, tt.out, got, tt.want)
			}
		})
	}
}

// 输出为空且非超时时原样返回运行错误,保留错误链。
func TestInterpretSecurityErrEmptyOutput(t *testing.T) {
	runErr := errors.New("exit status 1")
	if got := interpretSecurityErr(runErr, nil); !errors.Is(got, runErr) {
		t.Fatalf("interpretSecurityErr(runErr, nil) = %v, want 原错误", got)
	}
}

// 具体路径依赖宿主机钥匙串配置,只验证能解析出绝对路径。
func TestUserLoginKeychain(t *testing.T) {
	p, err := userLoginKeychain()
	if err != nil {
		t.Fatalf("userLoginKeychain() error: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Fatalf("userLoginKeychain() = %q, want 绝对路径", p)
	}
}
