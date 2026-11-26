// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package procinfo

import (
	"errors"
	"net"
	"sync/atomic"

	"github.com/mintfog/sniffy/pkg/process"
)

// 抓取范围的模式，与配置项 tunAppMode 的取值一致。
const (
	AppScopeAll     = "all"
	AppScopeInclude = "include"
	AppScopeExclude = "exclude"
)

var errOwnerFinderUnavailable = errors.New("当前平台不能按连接识别所属进程")

// AppFilter 按所属应用决定经虚拟网卡的连接是否进抓包链路，由 NewAppFilter 创建，可并发使用。
type AppFilter struct {
	finder   *process.OwnerFinder
	resolver *Resolver
	scope    atomic.Pointer[appScope]
}

type appScope struct {
	include bool
	apps    *process.AppSet
}

// NewAppFilter 创建按应用筛选的过滤器，初始全部抓取。resolver 是会话补全进程信息用的解析器，可为
// nil；只抓名单里的应用时，查到的所属进程转交给它，补全时不必再按连接查找。当前平台不能识别连接
// 所属的进程时 err 不为 nil，过滤器照常可用：查不到所属进程的连接按不在名单里处理。
func NewAppFilter(resolver *Resolver) (*AppFilter, error) {
	finder, err := process.NewOwnerFinder()
	return &AppFilter{finder: finder, resolver: resolver}, err
}

// SetScope 设置抓取范围，只影响此后的新连接。mode 为 AppScopeInclude 时只抓 entries 里的应用，
// AppScopeExclude 时抓它们以外的，其它取值全部抓取。entries 须已经 process.NormalizeAppEntries
// 规范化。
func (f *AppFilter) SetScope(mode string, entries []string) {
	switch mode {
	case AppScopeInclude, AppScopeExclude:
		f.scope.Store(&appScope{include: mode == AppScopeInclude, apps: process.NewAppSet(entries)})
	default:
		f.scope.Store(nil)
	}
}

// Capture 报告透明连接是否进抓包链路。client、target 是连接的 RemoteAddr 与 LocalAddr，即应用的
// 原始源地址与原始目标地址。
//
// 查不到所属进程（连接已经不在、所属进程无权查看、系统接口出错）的按不在名单里处理：只抓名单里的
// 应用时不抓，抓名单以外的应用时照抓。err 是查找出错的原因，供调用方记录。
func (f *AppFilter) Capture(client, target net.Addr) (bool, error) {
	s := f.scope.Load()
	if s == nil {
		return true, nil
	}
	if f.finder == nil {
		return !s.include, errOwnerFinderUnavailable
	}
	pid, owned, err := f.finder.FindOwner(client, target, s.apps)
	if owned && s.include {
		f.resolver.noteOwner(client, target, pid)
	}
	return owned == s.include, err
}
