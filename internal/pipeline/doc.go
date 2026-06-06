// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package pipeline 负责在一个 flow.Flow 上编排所有插件,并真正应用其返回的
// flow.Decision(continue / mock / abort / breakpoint)。
//
// 它由历史上的 plugins.HookExecutor 演进而来,保留按优先级排序与白/黑名单
// 门控,但:(1) 钩子契约升级为 flow.Flow + flow.Decision;(2) 修改真正生效;
// (3) 内置断点管理器用于暂停/放行。Go 原生层与 goja(JS)层都实现同一套钩子接口。
//
// 实现将在 P3 填充。
package pipeline
