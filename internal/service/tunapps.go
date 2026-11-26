// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"errors"
	"slices"

	"github.com/mintfog/sniffy/internal/procinfo"
	"github.com/mintfog/sniffy/pkg/process"
)

// tunScopeApps 返回当前模式用的那份应用名单，全部抓取时为 nil。
func (c AppConfig) tunScopeApps() []string {
	switch c.TunAppMode {
	case procinfo.AppScopeInclude:
		return c.TunIncludeApps
	case procinfo.AppScopeExclude:
		return c.TunExcludeApps
	}
	return nil
}

// SetTunAppsApplier 注入下发器并立即应用当前的抓取范围,与配置更新串行执行。下发的是当前模式用的那份名单。
func (s *Service) SetTunAppsApplier(fn func(mode string, apps []string)) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.applyTunApps = fn
	if fn != nil {
		c := s.cfg.get()
		fn(c.TunAppMode, c.tunScopeApps())
	}
}

// SetAppCatalog 接入列出可选程序与图标用的进程解析器(装配层在 New 之后调用)。
func (s *Service) SetAppCatalog(r *procinfo.Resolver) { s.apps = r }

// NetworkApps 列出此刻有网络连接的程序，供加入应用名单。
func (s *Service) NetworkApps() ([]procinfo.AppChoice, error) { return s.apps.NetworkApps() }

// InstalledApps 列出本机安装的应用，供加入应用名单。当前平台列不出时返回的错误满足
// errors.Is(err, errors.ErrUnsupported)。
func (s *Service) InstalledApps() ([]procinfo.AppChoice, error) { return procinfo.InstalledApps() }

// AppIcons 提取这些路径处程序的图标，提不出的不返回。列表先不带图标显示，图标随后补上：macOS 上每个
// 图标都要起子进程。
func (s *Service) AppIcons(paths []string) []procinfo.AppIcon { return s.apps.AppIcons(paths) }

// TunAppsRunning 报告名单里的每一项此刻有没有进程在运行，与 apps 一一对应。apps 是配置里的名单。
func (s *Service) TunAppsRunning(apps []string) ([]bool, error) { return process.RunningApps(apps) }

// SetTunAppCaptured 按会话右键的「只抓这个应用」（capture 为 true）或「不抓这个应用」改应用名单，返回
// 加入或移出名单的条目（见 process.AppEntry）。进程由可执行文件 path 与进程名 name 指明。
//
// 只抓：模式改为只抓名单里的应用，把它加入那份名单。不抓：正在只抓名单里的应用时，把选中它的项移出那份
// 名单；否则模式改为名单里的应用除外，把它加入那份名单。名单里已有选中它的项时不再加入。改后的名单不合
// 要求（超过上限，或条目不是有效的程序名）时报错，模式与名单都不改。
func (s *Service) SetTunAppCaptured(path, name string, capture bool) (string, AppConfig, error) {
	entry := process.AppEntry(path, name)
	if entry == "" {
		return "", s.Config(), errors.New("这条会话没有进程信息")
	}
	selects := func(e string) bool { return process.NewAppSet([]string{e}).Match(path, name) }

	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	c := s.cfg.get()
	mode, key, apps := procinfo.AppScopeInclude, "tunIncludeApps", c.TunIncludeApps
	switch {
	case capture:
		apps = withApp(apps, entry, selects)
	case c.TunAppMode == procinfo.AppScopeInclude:
		apps = slices.DeleteFunc(apps, selects)
	default:
		mode, key, apps = procinfo.AppScopeExclude, "tunExcludeApps", withApp(c.TunExcludeApps, entry, selects)
	}
	// 模式与名单须一起生效：名单在补丁里规范化失败时只会跳过它，模式照样写入。
	apps, err := process.NormalizeAppEntries(apps)
	if err != nil {
		return entry, c, err
	}
	return entry, s.updateConfigLocked(map[string]any{"tunAppMode": mode, key: apps}), nil
}

// withApp 在名单里还没有选中这个进程的项时，把 entry 加到末尾。
func withApp(apps []string, entry string, selects func(string) bool) []string {
	if slices.ContainsFunc(apps, selects) {
		return apps
	}
	return append(apps, entry)
}
