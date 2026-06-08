// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !desktop

// 默认（无 desktop 标签）构建下的占位入口，使 `go build` 在无 webview
// 依赖的环境中也能通过。真正的桌面程序请用 `-tags desktop` 构建。
package main

import "fmt"

func main() {
	fmt.Println("sniffy 桌面版(Wails v3)需以 `-tags desktop` 构建。")
	fmt.Println("先构建前端: cd web && npm run build")
	fmt.Println("再运行:     go run -tags desktop .")
	fmt.Println("生产构建:   CGO_ENABLED=0 go build -tags desktop -o sniffy-desktop.exe .  (或 scripts/build.sh desktop)")
	fmt.Println("")
	fmt.Println("如需 headless 服务器模式,请运行 cmd/sniffy。")
}
