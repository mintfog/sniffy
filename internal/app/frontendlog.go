// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"log"

	"github.com/mintfog/sniffy/internal/platform"
)

// frontendLogPrefix 前端日志文件名前缀，与后端 logFilePrefix("sniffy-") 区分，单独成文件。
const frontendLogPrefix = "sniffy-web-"

// NewFrontendLogger 返回写入独立前端日志文件 <LogsDir>/sniffy-web-<日期>.log 的 logger，
// 与后端日志（sniffy-<日期>.log）分文件，便于排查。复用按天滚动 / 体积上限 / 过期清理。
// 取不到日志目录时返回 nil，调用方应做空判断。
func NewFrontendLogger() *log.Logger {
	dir, err := platform.LogsDir()
	if err != nil {
		return nil
	}
	pruneOldLogsPrefixed(dir, frontendLogPrefix, logKeepDays)
	w := newRotatingFileWriter(dir)
	w.prefix = frontendLogPrefix
	return log.New(w, "", log.LstdFlags)
}
