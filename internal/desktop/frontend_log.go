// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"log"
	"sync"

	"github.com/mintfog/sniffy/internal/app"
)

// 前端日志写入独立文件（<LogsDir>/sniffy-web-<日期>.log），首次使用时惰性初始化。
var (
	frontendLogOnce sync.Once
	frontendLogger  *log.Logger
)

func frontendLog() *log.Logger {
	frontendLogOnce.Do(func() { frontendLogger = app.NewFrontendLogger() })
	return frontendLogger
}

// LogFrontend 把前端捕获的报错/警告写入独立的前端日志文件。
// level 取 "error"|"warn"|"info"，其余按 info；message 以 %s 传入，避免被当作格式串。
func (b *Bridge) LogFrontend(level, message string) {
	lg := frontendLog()
	if lg == nil {
		return
	}
	token := "[INFO]"
	switch level {
	case "error":
		token = "[ERROR]"
	case "warn":
		token = "[WARN]"
	}
	lg.Printf("%s %s", token, message)
}
