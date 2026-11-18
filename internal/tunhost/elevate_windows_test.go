// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"testing"
	"unsafe"
)

// ShellExecuteExW 按 cbSize 识别结构版本，布局错了会直接失败或读写越界。
func TestShellExecuteInfoSize(t *testing.T) {
	want := uintptr(60) // 32 位
	if unsafe.Sizeof(uintptr(0)) == 8 {
		want = 112
	}
	if got := unsafe.Sizeof(shellExecuteInfo{}); got != want {
		t.Errorf("shellExecuteInfo 是 %d 字节，SHELLEXECUTEINFOW 是 %d 字节", got, want)
	}
}
