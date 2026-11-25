// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package procinfo

import (
	"cmp"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/mintfog/sniffy/pkg/process"
)

// AppChoice 是可加入应用名单的一个程序。
type AppChoice struct {
	// Name 是显示名，与条目相同时为空。
	Name string `json:"name,omitempty"`
	// Entry 是加入名单用的条目，见 process.AppEntry。
	Entry string `json:"entry"`
	// Path 是可执行文件的路径，macOS 上已安装的应用是 .app 包的路径。
	Path     string `json:"path,omitempty"`
	IconData string `json:"iconData,omitempty"`
	IconType string `json:"iconType,omitempty"`
}

// AppIcon 是 Path 处程序的图标。
type AppIcon struct {
	Path     string `json:"path"`
	IconData string `json:"iconData"`
	IconType string `json:"iconType"`
}

// iconWorkers 是并发提取图标的数量：macOS 上每个图标要起两个子进程。
const iconWorkers = 8

// NetworkApps 列出此刻有 TCP 连接的程序，同一条目只列一次，按条目排序，不含 sniffy 自己与查不到的进程（见
// process.AppEntry）。要查看全部进程打开的连接，只在用户打开选择列表时调用。检测器顺带取到的图标（Windows）
// 一并带上，其余见 AppIcons。
func (r *Resolver) NetworkApps() ([]AppChoice, error) {
	if r == nil || r.detector == nil {
		return nil, errors.New("当前平台不能识别连接所属的进程")
	}
	conns, err := r.detector.GetAllConnections()
	if err != nil {
		return nil, err
	}
	self := uint32(os.Getpid())
	seen := make(map[string]bool)
	apps := []AppChoice{}
	for _, c := range conns {
		pi := c.ProcessInfo
		if pi == nil || pi.PID == self {
			continue
		}
		entry := process.AppEntry(pi.Path, pi.Name)
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		apps = append(apps, AppChoice{Entry: entry, Path: pi.Path, IconData: pi.IconData, IconType: pi.IconType})
	}
	slices.SortFunc(apps, func(a, b AppChoice) int { return cmp.Compare(strings.ToLower(a.Entry), strings.ToLower(b.Entry)) })
	return apps, nil
}

// InstalledApps 列出本机安装的应用，见 process.InstalledApps，不带图标。
func InstalledApps() ([]AppChoice, error) {
	installed, err := process.InstalledApps()
	if err != nil {
		return nil, err
	}
	apps := make([]AppChoice, len(installed))
	for i, a := range installed {
		apps[i] = AppChoice{Entry: a.Entry, Path: a.Path}
		if a.Name != a.Entry {
			apps[i].Name = a.Name
		}
	}
	return apps, nil
}

// AppIcons 提取这些路径处程序的图标，并发进行，提不出的不返回。
func (r *Resolver) AppIcons(paths []string) []AppIcon {
	if r == nil || r.icons == nil {
		return []AppIcon{}
	}
	jobs := make(chan string)
	var mu sync.Mutex
	icons := []AppIcon{}
	var wg sync.WaitGroup
	for range min(iconWorkers, len(paths)) {
		wg.Go(func() {
			for path := range jobs {
				icon, err := r.icons.ExtractIcon(path)
				if err != nil || icon == nil || !icon.HasIcon {
					continue
				}
				mu.Lock()
				icons = append(icons, AppIcon{Path: path, IconData: icon.IconData, IconType: icon.IconType})
				mu.Unlock()
			}
		})
	}
	for _, path := range slices.Compact(slices.Sorted(slices.Values(paths))) {
		if path != "" {
			jobs <- path
		}
	}
	close(jobs)
	wg.Wait()
	return icons
}
