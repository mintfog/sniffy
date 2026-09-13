// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package version 提供命令行、管理 API 与桌面端共用的应用版本。
package version

import "runtime/debug"

// DevVersion 用于既未注入版本号、也未携带模块版本的开发构建。
const DevVersion = "0.0.0-dev"

// Version 由发布构建注入，运行期间只读：
//
//	-ldflags "-X github.com/mintfog/sniffy/internal/version.Version=1.2.3"
var Version string

// Get 依次选用构建注入版本、go install 携带的模块版本、DevVersion。
func Get() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return DevVersion
}
