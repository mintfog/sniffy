// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// InstalledApp 是本机安装的一个应用，供加入应用名单。
type InstalledApp struct {
	// Name 是显示名，如开始菜单里的名字。
	Name string
	// Path 是可执行文件的路径，macOS 上是 .app 包的路径。
	Path string
	// Entry 是加入名单用的条目，见 AppEntry。
	Entry string
}

// InstalledApps 列出本机安装的应用，按显示名排序，同一程序只列一次：Windows 上取开始菜单里的程序与当前
// 用户的商店应用，macOS 上取各应用目录里的 .app 包。不同的程序可能条目相同（如都叫 Launcher.exe），各自
// 列出。其它平台返回的错误满足 errors.Is(err, errors.ErrUnsupported)。
func InstalledApps() ([]InstalledApp, error) {
	apps, err := installedApps()
	if err != nil {
		return nil, err
	}
	return uniqueApps(apps), nil
}

// uniqueApps 去掉条目为空的与路径重复的（路径按名单的匹配规则比较），按显示名排序。
func uniqueApps(apps []InstalledApp) []InstalledApp {
	seen := make(map[string]bool)
	unique := apps[:0]
	for _, app := range apps {
		key := hostAppRules.foldPath(app.Path)
		if app.Entry == "" || seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, app)
	}
	slices.SortFunc(unique, func(a, b InstalledApp) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return unique
}

// squirrelApp 返回 Squirrel 安装的应用真正运行的程序。这类应用的快捷方式指向应用目录下的 Update.exe，由参数
// --processStart X.exe 指明要启动的 X.exe：较新的版本在应用目录下放了同名的启动程序，否则 X.exe 在版本目录
// app-<版本号> 下。args 是切分好的参数。不是这类快捷方式时返回 target；找不到 X.exe 时返回的路径不存在。
func squirrelApp(target string, args []string) string {
	if !strings.EqualFold(filepath.Base(target), "Update.exe") {
		return target
	}
	i := slices.IndexFunc(args, func(a string) bool {
		return strings.EqualFold(a, "--processStart") || strings.EqualFold(a, "--processStartAndWait")
	})
	if i < 0 || i+1 == len(args) {
		return target
	}
	exe := args[i+1]
	if exe != filepath.Base(exe) || !strings.EqualFold(filepath.Ext(exe), ".exe") {
		return target
	}
	dir := filepath.Dir(target)
	candidates := []string{filepath.Join(dir, exe)}
	entries, _ := os.ReadDir(dir)
	for _, e := range slices.Backward(entries) {
		if e.IsDir() && strings.HasPrefix(e.Name(), "app-") {
			candidates = append(candidates, filepath.Join(dir, e.Name(), exe))
		}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return candidates[0]
}

// appBundles 列出 dirs 里的 .app 包，连同其下一层子目录里的（如 /Applications/Utilities、
// /Applications/Adobe Photoshop 2025）。条目是包名，覆盖包内的全部辅助进程。
func appBundles(dirs []string) []InstalledApp {
	var apps []InstalledApp
	var scan func(dir string, depth int)
	scan = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			name, isBundle := strings.CutSuffix(e.Name(), ".app")
			switch {
			case isBundle && name != "":
				apps = append(apps, InstalledApp{Name: name, Path: path, Entry: name})
			case e.IsDir() && depth == 0:
				scan(path, 1)
			}
		}
	}
	for _, dir := range dirs {
		scan(dir, 0)
	}
	return apps
}
