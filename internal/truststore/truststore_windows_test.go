// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package truststore

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

// PowerShell 单引号字面量内的单引号以双写转义。
func TestPSSingleQuoted(t *testing.T) {
	tests := []struct{ in, want string }{
		{`C:\Temp\sniffy-ca.crt`, `'C:\Temp\sniffy-ca.crt'`},
		{"", "''"},
		{"a'b", "'a''b'"},
		{"''", "''''''"},
		{`C:\Users\O'Brien\AppData\Local\Temp`, `'C:\Users\O''Brien\AppData\Local\Temp'`},
		{`"double"`, `'"double"'`},
	}
	for _, tt := range tests {
		if got := psSingleQuoted(tt.in); got != tt.want {
			t.Fatalf("psSingleQuoted(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPSSingleQuotedRoundTripThroughPowerShell(t *testing.T) {
	tests := []string{
		`C:\Temp\sniffy-ca.crt`,
		"",
		"a'b",
		"''",
		`C:\Users\O'Brien\AppData\Local\Temp\a'b'c.crt`,
		"$env:TEMP `n `$(id) 中文",
	}
	for _, in := range tests {
		// 模块初始化进度会以 CLIXML 写入 stderr;输出编码需与 Go 字符串一致。
		script := "$ProgressPreference = 'SilentlyContinue'; " +
			"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Write-Output " + psSingleQuoted(in)
		out := runCommand(t, "powershell", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text",
			"-EncodedCommand", encodedCommand(script))
		got := strings.TrimSuffix(out, "\r\n")
		if got != in {
			t.Errorf("PowerShell 还原 psSingleQuoted(%q) = %q", in, got)
		}
	}
}

// encodedCommand 按 powershell -EncodedCommand 要求把脚本编成 UTF-16LE 的 base64。
func encodedCommand(script string) string {
	var data []byte
	for _, unit := range utf16.Encode([]rune(script)) {
		data = binary.LittleEndian.AppendUint16(data, unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func TestInstallCanceledMarkerIsStable(t *testing.T) {
	if installCanceledMarker != "__SNIFFY_CANCELED_BY_USER__" {
		t.Fatalf("取消标记 = %q,与提权脚本写出的字面量不一致", installCanceledMarker)
	}
}

// 校验对取消标记的识别与分支优先级:用户取消 > 超时;未识别输出保留原文以免丢失线索。
func TestInterpretPSErr(t *testing.T) {
	exitErr := errors.New("exit status 2")
	tests := []struct {
		name   string
		runErr error
		out    string
		want   string
	}{
		{"取消标记", exitErr, installCanceledMarker + "\r\n", "truststore:canceled"},
		{"标记混在其它输出中", exitErr, "some text\r\n" + installCanceledMarker, "truststore:canceled"},
		{"取消优先于超时", context.DeadlineExceeded, installCanceledMarker, "truststore:canceled"},
		{"超时", context.DeadlineExceeded, "", "truststore:timeout:60"},
		{"未识别输出保留原文并去首尾空白", exitErr, "  certutil exited 1 \r\n", "certutil exited 1"},
		{"多行输出保留内部换行", exitErr, "\r\nline1\r\nline2\r\n", "line1\r\nline2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpretPSErr(tt.runErr, []byte(tt.out))
			if got == nil || got.Error() != tt.want {
				t.Fatalf("interpretPSErr(%v, %q) = %v, want %q", tt.runErr, tt.out, got, tt.want)
			}
		})
	}
}

// 输出为空且非超时时原样返回运行错误,保留错误链。
func TestInterpretPSErrEmptyOutput(t *testing.T) {
	runErr := errors.New("exit status 1")
	if got := interpretPSErr(runErr, nil); !errors.Is(got, runErr) {
		t.Fatalf("interpretPSErr(runErr, nil) = %v, want 原错误", got)
	}
}

// 提权与 UAC 交互需在桌面会话中验证。
func TestInstallScript(t *testing.T) {
	tests := []struct {
		name     string
		certPath string
	}{
		{"常规临时路径", `C:\Users\foo\AppData\Local\Temp\sniffy-ca-123\sniffy-ca.crt`},
		{"路径含空格", `C:\Program Files\sniffy\sniffy-ca.crt`},
		{"路径含单引号", `C:\Users\O'Brien\AppData\Local\Temp\sniffy-ca.crt`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := installScript(tt.certPath)

			for _, want := range []string{
				"Start-Process",
				"-Verb RunAs",
				"-Wait",
				"'certutil.exe'",
				"'Root'",
				"$_.Exception.NativeErrorCode -eq 1223",
				installCanceledMarker,
				"exit 2",
			} {
				if !strings.Contains(script, want) {
					t.Errorf("脚本缺少 %q:\n%s", want, script)
				}
			}
			if !strings.Contains(script, psSingleQuoted(tt.certPath)) {
				t.Errorf("脚本未以字面量注入证书路径 %q:\n%s", tt.certPath, script)
			}
		})
	}
}

// 提权解释器缺失时必须报错,不能静默认为安装成功。
func TestInstallWithoutPowerShell(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := Install(testCertPEM(t)); err == nil {
		t.Fatal("powershell 缺失时应返回错误")
	}
}
