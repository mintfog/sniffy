// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package nativeexample 是一个 Go 原生(编译进二进制)插件的参考实现。
//
// 它默认不会运行:Go 的 init() 只在包被引入构建图时执行。要启用,在装配处
// (internal/app/bootstrap.go)加一行 blank import:
//
//	import _ "github.com/mintfog/sniffy/examples/native_plugin"
//
// 装配层会在创建管道后遍历 native.All() 并用 RegisterCore 注册全部原生插件,
// 因此它不会被 JS 插件热重载清掉。详见 docs/plugins.md。
package nativeexample

import (
	"context"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/plugin/native"
)

// addHeader 给每个响应注入一个 X-Sniffy-Native 头,演示最小的原生插件。
type addHeader struct{}

func (addHeader) Name() string      { return "native-add-header" }
func (addHeader) Priority() int     { return 100 } // 数值越小越先执行
func (addHeader) Enabled() bool     { return true }
func (addHeader) Match(string) bool { return true } // 作用于所有 URL

// OnResponse 就地改写响应并标记 Modified;返回 Continue 表示放行(应用改动)。
func (addHeader) OnResponse(_ context.Context, f *flow.Flow) flow.Decision {
	if f.Response != nil {
		if f.Response.Header == nil {
			f.Response.Header = map[string][]string{}
		}
		f.Response.Header["X-Sniffy-Native"] = []string{"hello"}
		f.Modified = true
	}
	return flow.ContinueDecision()
}

func init() { native.Register(addHeader{}) }
