// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package plugin 实现两层插件系统(项目核心差异化卖点):
//
//   - native(Go 原生层):编译进二进制,用于内置 / 高性能 / 可信插件,
//     例如把每个 flow 喂给 service 的内置抓包插件 CaptureInterceptor。
//   - js(goja 层):用户在 UI 的 Monaco 编辑器里写 JavaScript,
//     运行时沙箱加载、热重载,任何人都能写插件改请求/改响应/mock/断点。
//
// 两层都实现同一套(在 internal/pipeline 中定义的)钩子接口,统一注册进管道。
// 本包还负责插件磁盘格式(plugin.json + 脚本)、发现与 fsnotify 热加载。
//
// 实现将在 P3 填充。
package plugin
