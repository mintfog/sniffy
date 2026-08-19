// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package pipeline 负责在一个 flow.Flow 上编排所有插件,并真正应用其返回的
// flow.Decision(continue / mock / abort / breakpoint)。
//
// 钩子按优先级排序执行,并受白/黑名单门控;内置断点管理器用于暂停/放行。
// Go 原生层(internal/plugin/native)与 goja(JS)层都实现同一套钩子接口。
package pipeline
