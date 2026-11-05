// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package update

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// RunningAppBundle 返回正在运行的 macOS 应用包路径;不在应用包内或不是 macOS 时返回空串。
// 返回非空时与 Wails 替换器的目标一致(不解析符号链接),否则可写性判断与暂存清理会落到它不替换的位置。
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

// BackupInTheWay 报告 <应用包>.bak 是否已存在:Wails 辅助进程备份原应用前会先删除该路径。
// 无法确认不存在时也视为存在。
func BackupInTheWay(bundle string) bool {
	_, err := os.Lstat(bundle + ".bak")
	return !errors.Is(err, fs.ErrNotExist)
}

// appBundleOf 返回 exe 所在的应用包,即路径中第一个以 .app 结尾的段;exe 不是该包的
// Contents/MacOS/<名字> 时返回空串。Wails 按第一个 .app 段整体替换,应用包嵌在其他 .app 内时
// 被替换的会是外层包。
func appBundleOf(exe string) string {
	parts := strings.Split(filepath.Clean(exe), string(filepath.Separator))
	i := slices.IndexFunc(parts, func(p string) bool { return strings.HasSuffix(p, ".app") })
	if i < 0 || i != len(parts)-4 || parts[i+1] != "Contents" || parts[i+2] != "MacOS" {
		return ""
	}
	return string(filepath.Separator) + filepath.Join(parts[1:i+1]...)
}
