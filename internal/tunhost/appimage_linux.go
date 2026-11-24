// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// launchedAppImage 是运行着本进程的 AppImage 文件，不在 AppImage 里运行时为 nil。在包初始化时记下：
// 越早记，越不会把启动之后才替换上来的文件当成运行着本进程的那个。
var launchedAppImage = currentAppImage()

func currentAppImage() *appImage {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	return detectAppImage(os.Getenv("APPIMAGE"), resolveMountDir(os.Getenv("APPDIR")), exe)
}

// resolveMountDir 解析挂载点 appDir 所在目录里的符号链接：挂载点在临时目录下，临时目录本身可能是
// 符号链接，而可执行文件路径（os.Executable、/proc/<pid>/exe）是解析过的。只解析上级目录，因为别的
// 用户（含 root）访问不了不带 allow_other 的 FUSE 挂载点本身。
func resolveMountDir(appDir string) string {
	if appDir == "" {
		return ""
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(appDir))
	if err != nil {
		return appDir
	}
	return filepath.Join(parent, filepath.Base(appDir))
}

// checkPeerAppImage 确认进程 pid 运行在 AppImage 文件 self 里，依据是它启动时的 APPIMAGE、APPDIR 与
// 它的可执行文件路径。
//
// 对端的可执行文件在它自己的 FUSE 挂载里，访问不了，只能这样间接判断。环境变量由启动对端的一方决定，
// 这比比对可执行文件弱：同一用户的程序只要让 APPIMAGE 指向这个 AppImage 文件、从 APPDIR 之下运行就能
// 通过。
func checkPeerAppImage(pid int, self *appImage) error {
	proc := "/proc/" + strconv.Itoa(pid)
	environ, err := os.ReadFile(proc + "/environ")
	if err != nil {
		return fmt.Errorf("读取进程 %d 的环境变量: %w", pid, err)
	}
	var image, appDir string
	for _, kv := range bytes.Split(environ, []byte{0}) {
		if v, ok := bytes.CutPrefix(kv, []byte("APPIMAGE=")); ok {
			image = string(v)
		} else if v, ok := bytes.CutPrefix(kv, []byte("APPDIR=")); ok {
			appDir = string(v)
		}
	}
	exe, err := os.Readlink(proc + "/exe")
	if err != nil {
		return fmt.Errorf("读取进程 %d 的可执行文件路径: %w", pid, err)
	}
	peer := detectAppImage(image, resolveMountDir(appDir), exe)
	if peer == nil {
		return fmt.Errorf("进程 %d 不是从 AppImage 运行的", pid)
	}
	if !os.SameFile(peer.file, self.file) {
		return fmt.Errorf("进程 %d 运行的不是 %s", pid, self.path)
	}
	return nil
}
