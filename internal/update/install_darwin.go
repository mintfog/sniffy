// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && darwin

package update

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	installerAction  = ActionOpen
	appArchiveAction = ActionReplace
)

// appReplaceable 测试时可替换。
var appReplaceable = runningAppReplaceable

// runningAppReplaceable 判断正在运行的应用包能否被原地替换:新包暂存在应用所在目录,
// 退出后改名换入。从安装镜像或 App Translocation 的只读挂载运行时写权限检查不通过。
func runningAppReplaceable() bool {
	bundle := RunningAppBundle()
	if bundle == "" {
		return false
	}
	writable := unix.Access(bundle, unix.W_OK) == nil && unix.Access(filepath.Dir(bundle), unix.W_OK) == nil
	return writable && !BackupInTheWay(bundle)
}
