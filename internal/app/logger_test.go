// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/platform"
)

func preserveAppLogging(t *testing.T) {
	t.Helper()
	out, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	fileLogMu.Lock()
	previous := fileLogWriter
	fileLogMu.Unlock()
	t.Cleanup(func() {
		log.SetOutput(out)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
		fileLogMu.Lock()
		current := fileLogWriter
		fileLogWriter = previous
		fileLogMu.Unlock()
		if current != nil && current != previous {
			current.Close()
		}
	})
}

func TestLoggerLevels(t *testing.T) {
	preserveAppLogging(t)
	log.SetFlags(0)
	log.SetPrefix("")
	for _, verbose := range []bool{false, true} {
		var out bytes.Buffer
		log.SetOutput(&out)
		logger := NewLogger(verbose)
		logger.Info("请求 %d", 1)
		logger.Warn("请求 %d", 2)
		logger.Error("请求 %d", 3)
		logger.Debug("请求 %d", 4)
		want := "[INFO] 请求 1\n[WARN] 请求 2\n[ERROR] 请求 3\n"
		if verbose {
			want += "[DEBUG] 请求 4\n"
		}
		if got := out.String(); got != want {
			t.Errorf("verbose=%v 日志 = %q，期望 %q", verbose, got, want)
		}
	}
}

func TestFileLoggingSeparatesFrontend(t *testing.T) {
	isolateAppDirs(t)
	preserveAppLogging(t)
	dir, err := EnableFileLogging()
	if err != nil {
		t.Fatal(err)
	}
	frontend := NewFrontendLogger()
	if frontend == nil {
		t.Fatal("前端日志器未创建")
	}
	w, ok := frontend.Writer().(*rotatingFileWriter)
	if !ok {
		t.Fatalf("前端日志写入器类型 = %T", frontend.Writer())
	}
	t.Cleanup(w.Close)
	// 固定日期，避免在午夜跨文件时产生偶发失败。
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return now }
	fileLogWriter.now = w.now
	log.Print("backend-first")
	log.Print("backend-buffered")
	frontend.Print("frontend-first")
	frontend.Print("frontend-buffered")
	FlushLogs()
	w.Flush()
	for _, tt := range []struct{ prefix, want, forbidden string }{
		{logFilePrefix, "backend-buffered", "frontend-"},
		{frontendLogPrefix, "frontend-buffered", "backend-"},
	} {
		data, err := os.ReadFile(filepath.Join(dir, tt.prefix+now.Format(logDayFormat)+logFileSuffix))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), tt.want) || strings.Contains(string(data), tt.forbidden) {
			t.Errorf("%s 日志内容错误: %s", tt.prefix, data)
		}
	}
}

func TestFileLoggingReplacesWriter(t *testing.T) {
	isolateAppDirs(t)
	preserveAppLogging(t)
	if _, err := EnableFileLogging(); err != nil {
		t.Fatal(err)
	}
	previous := fileLogWriter
	t.Cleanup(previous.Close)
	// 让第二条日志留在缓冲中，验证切换输出时会主动落盘。
	previous.flushDelay = time.Hour
	previous.writeThrough = time.Hour
	log.Print("切换前首条日志")
	log.Print("切换前缓冲日志")
	previousFile := previous.file

	if _, err := EnableFileLogging(); err != nil {
		t.Fatal(err)
	}
	// Windows 的 Stat 会返回原生无效句柄错误，零字节写入可跨平台检查 os.ErrClosed。
	if _, err := previousFile.Write(nil); !errors.Is(err, os.ErrClosed) {
		t.Errorf("切换后旧日志文件仍未关闭: %v", err)
	}
	data, err := os.ReadFile(previousFile.Name())
	if err != nil || !strings.Contains(string(data), "切换前缓冲日志") {
		t.Errorf("旧写入器的缓冲未落盘: %q, %v", data, err)
	}

	log.Print("切换后日志")
	FlushLogs()
	data, err = os.ReadFile(fileLogWriter.file.Name())
	if err != nil || !strings.Contains(string(data), "切换后日志") {
		t.Errorf("新写入器未接收日志: %q, %v", data, err)
	}
}

func TestFileLoggingUnavailableDirectory(t *testing.T) {
	isolateAppDirs(t)
	preserveAppLogging(t)
	dir, err := platform.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	writeAppFixture(t, filepath.Join(dir, "logs"), "占用日志目录路径")
	var out bytes.Buffer
	log.SetOutput(&out)
	if dir, err := EnableFileLogging(); err == nil || dir != "" {
		t.Fatalf("期望启用失败，得到 (%q, %v)", dir, err)
	}
	if log.Writer() != &out {
		t.Fatal("启用失败后应保留原日志出口")
	}
	if got := NewFrontendLogger(); got != nil {
		t.Fatal("日志目录不可用时前端日志器应为空")
	}
}

func TestFatalfFlushesBeforeExit(t *testing.T) {
	if inAppTestSubprocess(t) {
		if _, err := EnableFileLogging(); err != nil {
			t.Fatal(err)
		}
		// 延长定时器以保证退出前由 Fatalf 主动提交缓冲。
		fileLogWriter.flushDelay = time.Hour
		fileLogWriter.writeThrough = time.Hour
		log.Print("fatal-prefill")
		Fatalf("启动失败: %s", "fatal-sentinel")
		return
	}
	isolateAppDirs(t)
	out, err := appTestSubprocess(t)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("退出结果 = %v，期望退出码 1\n%s", err, out)
	}
	dir, err := platform.LogsDir()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, logFilePrefix+"*"+logFileSuffix))
	if err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		logs.Write(data)
	}
	if !strings.Contains(logs.String(), "[FATAL] 启动失败: fatal-sentinel") || !strings.Contains(logs.String(), "fatal-prefill") {
		t.Fatalf("退出前日志未完整落盘: %q", logs.String())
	}
}
