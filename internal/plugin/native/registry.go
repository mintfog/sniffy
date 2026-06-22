// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package native 是 Go 原生(编译进二进制)插件的注册表。
//
// 为什么是「编译进二进制」而非 .so 动态加载:Go 的 plugin 包不支持 Windows,且对编译器/
// 依赖版本极敏感,跨平台不可靠。Sniffy 的 Go 插件改为与主程序一起编译——开发者实现
// pipeline.Hook(及所需的阶段接口),在 init() 里调用 native.Register 自注册,并确保其包被
// 引入构建图(在装配处加一行 blank import 即可)。装配层启动时遍历 All() 注册进管道。
//
// 用法:
//
//	package myplugin
//	import (
//	    "context"
//	    "github.com/mintfog/sniffy/internal/flow"
//	    "github.com/mintfog/sniffy/internal/plugin/native"
//	)
//	type addHeader struct{}
//	func (addHeader) Name() string         { return "add-header" }
//	func (addHeader) Priority() int        { return 100 }
//	func (addHeader) Enabled() bool        { return true }
//	func (addHeader) Match(string) bool    { return true }
//	func (addHeader) OnResponse(_ context.Context, f *flow.Flow) flow.Decision {
//	    if f.Response != nil { f.Response.Header["X-Sniffy"] = []string{"hi"}; f.Modified = true }
//	    return flow.ContinueDecision()
//	}
//	func init() { native.Register(addHeader{}) }
package native

import (
	"sync"

	"github.com/mintfog/sniffy/internal/pipeline"
)

var (
	mu    sync.Mutex
	hooks []pipeline.Hook
)

// Register 登记一个 Go 原生插件。通常在插件包的 init() 中调用。
func Register(h pipeline.Hook) {
	mu.Lock()
	defer mu.Unlock()
	hooks = append(hooks, h)
}

// All 返回当前已注册的全部原生插件的副本。
func All() []pipeline.Hook {
	mu.Lock()
	defer mu.Unlock()
	return append([]pipeline.Hook(nil), hooks...)
}
