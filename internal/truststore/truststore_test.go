// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package truststore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"
)

// 与 web/src/workbench/lib/backendError.ts 中 CODED_ERROR 的格式契约一致。
var frontendCodeRe = regexp.MustCompile(`^truststore:([a-z_]+)(?::([\s\S]+))?$`)

var errorCodeCases = []struct {
	code   string
	want   string
	detail string
}{
	{CodeCanceled, "canceled", ""},
	{CodeTimeout, "timeout", "90"},
	{CodeTrustSettings, "trust_settings", "SecTrustSettingsSetTrustSettings: unknown error"},
	{CodeKeychain, "keychain", "user: Current requires cgo"},
	{CodePkexecMissing, "pkexec_missing", ""},
	{CodeUnsupportedDistro, "unsupported_distro", ""},
	{CodeUnsupported, "unsupported", ""},
}

func TestErrorSerialization(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{"无 Detail 序列化", &Error{Code: CodeCanceled}, "truststore:canceled"},
		{"带 Detail 序列化", &Error{Code: CodeTimeout, Detail: "60"}, "truststore:timeout:60"},
		{"空 Code 仍带前缀", &Error{}, "truststore:"},
		{
			"Detail 含冒号",
			&Error{Code: CodeKeychain, Detail: "SecKeychain: not found"},
			"truststore:keychain:SecKeychain: not found",
		},
		{
			"Detail 含换行",
			&Error{Code: CodeTrustSettings, Detail: "SecTrustSettings:\nunknown"},
			"truststore:trust_settings:SecTrustSettings:\nunknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorWireFormatMatchesFrontendParser(t *testing.T) {
	for _, tc := range errorCodeCases {
		code, detail := tc.code, tc.detail
		serialized := (&Error{Code: code, Detail: detail}).Error()
		m := frontendCodeRe.FindStringSubmatch(serialized)
		if m == nil {
			t.Errorf("码 %q 序列化为 %q,前端解析失败", code, serialized)
			continue
		}
		if m[1] != code || m[2] != detail {
			t.Errorf("%q 解析为码 %q / detail %q, want %q / %q", serialized, m[1], m[2], code, detail)
		}
	}
}

// 安装错误码与三套语言包共享契约,插值名称也需一致。
func TestInstallErrorLocaleCoverage(t *testing.T) {
	codes := make(map[string]bool, len(errorCodeCases))
	for _, tc := range errorCodeCases {
		codes[tc.code] = true
	}
	localeDir := filepath.Join("..", "..", "web", "src", "i18n", "locales")
	base := installErrorEntries(t, filepath.Join(localeDir, "en.json"))

	for _, locale := range []string{"en", "zh-Hans", "zh-Hant"} {
		t.Run(locale, func(t *testing.T) {
			entries := base
			if locale != "en" {
				entries = installErrorEntries(t, filepath.Join(localeDir, locale+".json"))
			}
			for code := range codes {
				if _, ok := entries[code]; !ok {
					t.Errorf("缺少 certs.installErrors.%s", code)
				}
			}
			for code, text := range entries {
				if !codes[code] {
					t.Errorf("certs.installErrors.%s 在 Go 侧无对应码", code)
					continue
				}
				if baseText, ok := base[code]; ok && locale != "en" {
					if got, want := placeholders(text), placeholders(baseText); !slices.Equal(got, want) {
						t.Errorf("%s 占位符为 %v,与 en 的 %v 不一致", code, got, want)
					}
				}
			}
		})
	}
}

func TestCodeLiterals(t *testing.T) {
	for _, tc := range errorCodeCases {
		if tc.code != tc.want {
			t.Errorf("错误码 = %q, want %q", tc.code, tc.want)
		}
	}
}

func TestErrUnsupported(t *testing.T) {
	if ErrUnsupported == nil {
		t.Fatal("ErrUnsupported 为 nil")
	}
	if got := ErrUnsupported.Error(); got != "truststore:"+CodeUnsupported {
		t.Fatalf("ErrUnsupported.Error() = %q", got)
	}
}

func TestWriteTempCert(t *testing.T) {
	pem := testCertPEM(t)

	dir, path, err := writeTempCert(pem)
	if err != nil {
		t.Fatalf("writeTempCert() error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if filepath.Dir(path) != dir {
		t.Fatalf("证书路径 %q 不在返回目录 %q 下", path, dir)
	}
	if filepath.Base(path) != "sniffy-ca.crt" {
		t.Fatalf("证书文件名 = %q, want sniffy-ca.crt", filepath.Base(path))
	}

	st, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("统计临时目录失败: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Errorf("临时目录权限 = %v, want 0700", st.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取临时目录失败: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "sniffy-ca.crt" {
		t.Fatalf("临时目录内容 = %v, want 仅 sniffy-ca.crt", entries)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取写出的证书失败: %v", err)
	}
	if !bytes.Equal(got, pem) {
		t.Fatalf("写出内容不一致: %q", got)
	}
}

func TestWriteTempCertIsolatesCalls(t *testing.T) {
	pem := testCertPEM(t)
	dirs := make([]string, 0, 8)
	for i := range 8 {
		dir, path, err := writeTempCert(pem)
		if err != nil {
			t.Fatalf("第 %d 次 writeTempCert() error: %v", i, err)
		}
		defer os.RemoveAll(dir)
		dirs = append(dirs, dir)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("第 %d 次未写出证书: %v", i, err)
		}
	}
	for i, dir := range dirs {
		if slices.Contains(dirs[:i], dir) {
			t.Fatalf("目录 %q 被重复返回", dir)
		}
	}
}

func TestWriteTempCertEmptyPayload(t *testing.T) {
	dir, path, err := writeTempCert(nil)
	if err != nil {
		t.Fatalf("writeTempCert(nil) error: %v", err)
	}
	defer os.RemoveAll(dir)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("证书文件缺失: %v", err)
	}
	if st.Size() != 0 {
		t.Fatalf("空载荷写出的长度 = %d, want 0", st.Size())
	}
}

func TestWriteTempCertTempDirUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	// 覆盖各平台 os.TempDir 依赖的全部环境变量。
	for _, k := range []string{"TMPDIR", "TMP", "TEMP", "USERPROFILE"} {
		t.Setenv(k, missing)
	}
	if _, _, err := writeTempCert([]byte("x")); err == nil {
		t.Fatal("临时目录不可用时应返回错误")
	}
}

func TestWriteTempCertReadOnlyTempDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("依赖 POSIX 目录写权限语义,且 root 会绕过检查")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, _, err := writeTempCert([]byte("x")); err == nil {
		t.Fatal("临时目录只读时应返回错误")
	}
}

// installErrorEntries 读取语言包里的 certs.installErrors 映射。
func installErrorEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取语言包失败: %v", err)
	}
	var doc struct {
		Certs struct {
			InstallErrors map[string]string `json:"installErrors"`
		} `json:"certs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}
	if len(doc.Certs.InstallErrors) == 0 {
		t.Fatalf("%s 中 certs.installErrors 为空", path)
	}
	return doc.Certs.InstallErrors
}

var placeholderRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

func placeholders(text string) []string {
	var found []string
	for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
		found = append(found, m[1])
	}
	slices.Sort(found)
	return found
}
