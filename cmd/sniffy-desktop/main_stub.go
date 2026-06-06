// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !desktop

// 默认(无 desktop 标签)构建下的占位入口,使 `go build ./...` 在无 webview
// 依赖的环境中也能通过。真正的桌面程序请用 `-tags desktop` 构建。
package main

import "fmt"

func main() {
	fmt.Println("sniffy-desktop 需以 `-tags desktop` 构建(并安装对应平台的 webview 依赖)。")
	fmt.Println("请使用 `wails build` 或 scripts/build.sh desktop。")
}
