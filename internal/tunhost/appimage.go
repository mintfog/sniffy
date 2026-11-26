// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"fmt"
	"os"
	"path/filepath"
)

// appImage 是运行着某个进程的 AppImage 文件。
//
// AppImage 运行时把程序所在的文件系统挂成只有当前用户能访问的 FUSE（不带 allow_other），root 也访问
// 不了里面的文件：helper 要从 AppImage 文件本身拉起（运行时以 root 身份另挂一份），通道两端也没法比对
// 各自的可执行文件，只能比对运行它们的 AppImage 文件，见 checkPeerAppImage。
type appImage struct {
	path string
	file os.FileInfo
}

// detectAppImage 按 AppImage 运行时设下的 APPIMAGE（AppImage 文件）与 APPDIR（挂载点）判断可执行文件
// exe 是否运行在 AppImage 里，不是时返回 nil。只看路径字面：exe 须在 appDir 之下，否则这两个环境变量是
// 从别的 AppImage 程序继承来的。
func detectAppImage(image, appDir, exe string) *appImage {
	if image == "" || appDir == "" {
		return nil
	}
	rel, err := filepath.Rel(appDir, exe)
	if err != nil || !filepath.IsLocal(rel) {
		return nil
	}
	info, err := os.Stat(image)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return &appImage{path: image, file: info}
}

// checkUnchanged 确认 AppImage 文件此刻仍是记下的那个，同 checkRunningImage。
func (a *appImage) checkUnchanged() error {
	info, err := os.Stat(a.path)
	if err != nil {
		return err
	}
	if !os.SameFile(a.file, info) {
		return fmt.Errorf("%s 在 sniffy 启动后被替换（如已更新），请重启 sniffy 后再开启", a.path)
	}
	return nil
}

// helperExecutable 返回拉起 helper 用的可执行文件：在 AppImage 里运行时是 AppImage 文件，否则是本进程的
// 可执行文件。要在提权确认之前发现它已被替换：按路径拉起的 helper 运行的是替换后的文件，会拒绝本进程。
func helperExecutable() (string, error) {
	if launchedAppImage != nil {
		return launchedAppImage.path, launchedAppImage.checkUnchanged()
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位可执行文件: %w", err)
	}
	return exe, checkRunningImage(exe)
}

// checkPeerProgram 确认通道对端进程 pid 运行的是与本进程相同的程序：在 AppImage 里运行时比对 AppImage
// 文件，否则比对可执行文件。
func checkPeerProgram(pid int) error {
	if launchedAppImage != nil {
		return checkPeerAppImage(pid, launchedAppImage)
	}
	return checkSameExecutable(pid)
}
