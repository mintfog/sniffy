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
	"strings"
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
		{"首尾空白与多行", "\n  \"/Users/foo/login.keychain-db\"  \n\n", "/Users/foo/login.keychain-db"},
		{"路径含空格", `"/Users/foo bar/Library/Keychains/login.keychain-db"`, "/Users/foo bar/Library/Keychains/login.keychain-db"},
		{"路径含单引号", `"/Users/O'Brien/Library/Keychains/login.keychain-db"`, "/Users/O'Brien/Library/Keychains/login.keychain-db"},
		{"空输出", "", ""},
		{"仅空白", "  \n\t", ""},
		{"非绝对路径", `"login.keychain-db"`, ""},
		{"相对路径", `"Library/Keychains/login.keychain-db"`, ""},
		{"不以斜杠开头", "no-such-keychain", ""},
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
		{"authorization was canceled 锚点", exitErr, "Authorization was canceled", "truststore:canceled"},
		{"取消优先于信任设置失败", exitErr, "SecTrustSettingsSetTrustSettings: canceled (-128)", "truststore:canceled"},
		{"信任设置失败携带原文", exitErr, "SecTrustSettingsSetTrustSettings: unknown error", "truststore:trust_settings:SecTrustSettingsSetTrustSettings: unknown error"},
		{"trust settings 锚点", exitErr, "could not change trust settings", "truststore:trust_settings:could not change trust settings"},
		{"信任设置错误同样去首尾空白", exitErr, "  SecTrustSettings: bad\n", "truststore:trust_settings:SecTrustSettings: bad"},
		{"取消优先于超时", context.DeadlineExceeded, "user canceled", "truststore:canceled"},
		{"超时", context.DeadlineExceeded, "", "truststore:timeout:90"},
		{"未识别输出保留原文并去首尾空白", exitErr, "  some weird failure \n", "some weird failure"},
		{"多行输出保留内部换行", exitErr, "\nfirst\nsecond\n", "first\nsecond"},
		{"钥匙串原始错误", exitErr, "SecKeychainItemCreateFromContent: incorrect passphrase", "SecKeychainItemCreateFromContent: incorrect passphrase"},
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

// security 返回宿主机默认钥匙串时无法覆盖回退路径,需跳过该场景。
func TestUserLoginKeychainFallbackOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present []string
		want    string
	}{
		{"新格式优先", []string{"login.keychain-db", "login.keychain"}, "login.keychain-db"},
		{"仅旧格式", []string{"login.keychain"}, "login.keychain"},
		{"均不存在", nil, "login.keychain-db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			base := filepath.Join(home, "Library", "Keychains")
			for _, name := range tc.present {
				writeFile(t, filepath.Join(base, name), nil)
			}

			got, err := userLoginKeychain()
			if err != nil {
				t.Fatalf("userLoginKeychain() error: %v", err)
			}
			if !strings.HasPrefix(got, home+string(filepath.Separator)) {
				t.Skipf("本机 security 返回了默认钥匙串 %q,回退分支不适用", got)
			}
			if want := filepath.Join(base, tc.want); got != want {
				t.Errorf("userLoginKeychain() = %q, want %q", got, want)
			}
		})
	}
}
