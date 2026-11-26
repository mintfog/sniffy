// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import "slices"

// runningImage 是一个正在运行的进程的可执行文件路径与进程名别名（见 AppSet.Match）。
type runningImage struct {
	path, name string
}

// RunningApps 报告名单里的每一项此刻有没有进程在运行，与 entries 一一对应。entries 须已经
// NormalizeAppEntries 规范化。本进程无权查看路径的进程只按进程名比较。
func RunningApps(entries []string) ([]bool, error) {
	images, err := runningImages()
	if err != nil {
		return nil, err
	}
	running := make([]bool, len(entries))
	for i, entry := range entries {
		apps := NewAppSet([]string{entry})
		running[i] = slices.ContainsFunc(images, func(img runningImage) bool { return apps.Match(img.path, img.name) })
	}
	return running, nil
}
