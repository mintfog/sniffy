// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package update

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RunningAppBundle 返回正在运行的 macOS 应用包路径;不在应用包内或不是 macOS 时返回空串。
// 推导规则与 Wails 替换器一致(不解析符号链接),否则可写性判断与暂存清理会落到它不替换的位置。
func RunningAppBundle() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return appBundleOf(exe)
}

// appBundleOf 取路径中第一个以 .app 结尾的段。
func appBundleOf(exe string) string {
	parts := strings.Split(filepath.Clean(exe), string(filepath.Separator))
	for i, p := range parts {
		if strings.HasSuffix(p, ".app") {
			return string(filepath.Separator) + filepath.Join(parts[1:i+1]...)
		}
	}
	return ""
}
