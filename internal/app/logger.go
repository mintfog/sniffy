// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import "log"

// Logger 是一个简单的标准库日志器,同时满足 types.Logger 与 pipeline.Logger。
type Logger struct {
	Verbose bool
}

// NewLogger 创建日志器。
func NewLogger(verbose bool) *Logger { return &Logger{Verbose: verbose} }

func (l *Logger) Info(msg string, args ...any)  { log.Printf("[INFO] "+msg, args...) }
func (l *Logger) Error(msg string, args ...any) { log.Printf("[ERROR] "+msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { log.Printf("[WARN] "+msg, args...) }
func (l *Logger) Debug(msg string, args ...any) {
	if l.Verbose {
		log.Printf("[DEBUG] "+msg, args...)
	}
}
