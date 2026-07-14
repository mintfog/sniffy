// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package truststore

import (
	"context"
	"errors"
	"testing"
)

// PowerShell 单引号字面量内的单引号以双写转义。
func TestPSSingleQuoted(t *testing.T) {
	tests := []struct{ in, want string }{
		{`C:\Temp\sniffy-ca.crt`, `'C:\Temp\sniffy-ca.crt'`},
		{"", "''"},
		{"a'b", "'a''b'"},
	}
	for _, tt := range tests {
		if got := psSingleQuoted(tt.in); got != tt.want {
			t.Fatalf("psSingleQuoted(%q) = %q, want %q", tt.in, got, tt.want)
		}
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
		{"超时", context.DeadlineExceeded, "", "truststore:timeout:60"},
		{"取消优先于超时", context.DeadlineExceeded, installCanceledMarker, "truststore:canceled"},
		{"未识别输出保留原文并去首尾空白", exitErr, "  certutil exited 1 \r\n", "certutil exited 1"},
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
