// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/platform"
)

// 日志落盘参数。写入路径的设计目标:热路径上一条日志的代价只是一次内存拷贝,
// 磁盘 syscall 由缓冲分摊;低频日志(启动/错误/退出)直写,进程随时退出也不丢。
const (
	logKeepDays      = 7                      // 日志文件保留天数
	logMaxDailyBytes = 64 << 20               // 单日体积上限,超出后丢弃当日剩余日志
	logBufSize       = 64 << 10               // 写缓冲大小,写满时 bufio 自动落盘
	logFlushDelay    = 200 * time.Millisecond // 突发缓冲后延迟落盘的最长间隔
	logWriteThrough  = 200 * time.Millisecond // 距上次写入超过该间隔视为低频,直写落盘
	logReopenBackoff = time.Second            // 打开日志文件失败后的重试间隔
)

const (
	logFilePrefix = "sniffy-"
	logFileSuffix = ".log"
	logDayFormat  = "2006-01-02"
)

// errLevelToken 出现在行内时立即落盘(app.Logger.Error 的统一前缀)。
var errLevelToken = []byte("[ERROR]")

// rotatingFileWriter 按天滚动、带自适应缓冲的日志文件写入器。
// 任何失败(打开/写入)都丢弃日志并返回成功,绝不把磁盘故障传导给调用方;
// 失败后按 reopenBackoff 退避重试,避免坏盘时每条日志都做无谓 syscall。
type rotatingFileWriter struct {
	dir    string
	prefix string           // 日志文件名前缀（默认 logFilePrefix；前端日志用独立前缀分文件）
	now    func() time.Time // 可注入时钟,便于测试

	// 以下参数从包常量取默认值,测试可在构造后覆盖。
	maxDaily      int64
	flushDelay    time.Duration
	writeThrough  time.Duration
	reopenBackoff time.Duration

	mu           sync.Mutex
	file         *os.File
	buf          *bufio.Writer
	nextRotate   time.Time // 下次跨天切换时间
	written      int64     // 当天已写字节数(含本进程启动前同日写入)
	capped       bool      // 当天已达体积上限
	lastWrite    time.Time
	flushPending bool      // 已安排延迟落盘,避免重复定时器
	reopenAfter  time.Time // 打开失败后,此时间点前不再重试
}

func newRotatingFileWriter(dir string) *rotatingFileWriter {
	return &rotatingFileWriter{
		dir:           dir,
		prefix:        logFilePrefix,
		now:           time.Now,
		maxDaily:      logMaxDailyBytes,
		flushDelay:    logFlushDelay,
		writeThrough:  logWriteThrough,
		reopenBackoff: logReopenBackoff,
	}
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()
	if w.file == nil || !now.Before(w.nextRotate) {
		if !w.rotate(now) {
			return len(p), nil
		}
	}
	if w.capped {
		return len(p), nil
	}
	if w.written += int64(len(p)); w.written > w.maxDaily {
		// 写出越界这一行再追加标记,使文件真正超过上限:重启后按文件大小重算
		// capped 仍为 true,不会"解封"而重复写标记、继续增长。
		_, _ = w.buf.Write(p)
		n, _ := fmt.Fprintf(w.buf, "==== 日志达到当日体积上限 %d MB,今日剩余日志将被丢弃 ====\n", w.maxDaily>>20)
		w.written += int64(n)
		_ = w.buf.Flush()
		w.capped = true
		return len(p), nil
	}
	if _, err := w.buf.Write(p); err != nil {
		// bufio 的写错误是"粘性"的:一旦发生,后续 Write/Flush 会永久返回同一错误
		// 而不再落盘。丢弃当前句柄,下次写入经退避后重开文件(rotate 内 buf.Reset 清错),
		// 否则一次瞬时磁盘故障会让当日日志(含 [ERROR])全部进黑洞直到跨天。
		w.fail(now)
		return len(p), nil
	}

	// 落盘策略:低频日志与错误日志直写;高频突发只安排一次延迟 Flush,
	// 把磁盘 syscall 分摊到多条日志上。
	idle := now.Sub(w.lastWrite) >= w.writeThrough
	w.lastWrite = now
	if idle || bytes.Contains(p, errLevelToken) {
		if err := w.buf.Flush(); err != nil {
			w.fail(now)
		}
	} else if !w.flushPending {
		w.flushPending = true
		time.AfterFunc(w.flushDelay, w.delayedFlush)
	}
	return len(p), nil
}

// fail 在写/落盘出错(含 bufio 粘性错误)时丢弃当前文件句柄并进入退避期,
// 使下次 Write 经 rotate 重开文件并 Reset 缓冲,从瞬时故障中自动恢复。
func (w *rotatingFileWriter) fail(now time.Time) {
	w.closeFile()
	w.reopenAfter = now.Add(w.reopenBackoff)
}

func (w *rotatingFileWriter) delayedFlush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushPending = false
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			w.fail(w.now())
		}
	}
}

// Flush 把缓冲中的日志立即落盘,供进程退出前调用。
func (w *rotatingFileWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			w.fail(w.now())
		}
	}
}

// Close 把缓冲落盘并关闭当前日志文件。之后再写入会自动重新打开。
func (w *rotatingFileWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeFile()
}

// rotate 切换到 now 所在日期的日志文件。失败时关闭旧文件、进入退避期并返回 false。
func (w *rotatingFileWriter) rotate(now time.Time) bool {
	if w.file == nil && now.Before(w.reopenAfter) {
		return false
	}
	day := now.Format(logDayFormat)
	f, err := os.OpenFile(filepath.Join(w.dir, w.prefix+day+logFileSuffix),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		w.closeFile()
		w.reopenAfter = now.Add(w.reopenBackoff)
		return false
	}
	w.closeFile() // 旧文件缓冲先落盘再关闭
	w.file = f
	if w.buf == nil {
		w.buf = bufio.NewWriterSize(f, logBufSize)
	} else {
		w.buf.Reset(f)
	}
	// 体积上限按文件现有大小累计:进程当日重启不会绕过上限。
	w.written = 0
	if fi, err := f.Stat(); err == nil {
		w.written = fi.Size()
	}
	w.capped = w.written > w.maxDaily
	y, m, d := now.Date()
	w.nextRotate = time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
	return true
}

func (w *rotatingFileWriter) closeFile() {
	if w.file == nil {
		return
	}
	if w.buf != nil {
		_ = w.buf.Flush()
	}
	_ = w.file.Close()
	w.file = nil
}

// 包级持有当前文件写入器,供进程退出前 FlushLogs。
var (
	fileLogMu     sync.Mutex
	fileLogWriter *rotatingFileWriter
)

// EnableFileLogging 把标准库 log(本项目所有日志的最终出口)改为写入日志目录下
// 按天滚动的文件,控制台可用时同时输出,并清理过期日志。返回日志目录。
// 失败时不改动现有输出,调用方可降级为仅控制台。
func EnableFileLogging() (string, error) {
	dir, err := platform.LogsDir()
	if err != nil {
		return "", err
	}
	pruneOldLogs(dir, logKeepDays)

	w := newRotatingFileWriter(dir)
	fileLogMu.Lock()
	fileLogWriter = w
	fileLogMu.Unlock()

	// Windows GUI 子系统(-H windowsgui)下 stderr 句柄无效,逐条写必败、白费
	// syscall,启动时探测一次,不可用则只写文件。可用时文件写入器必须排在前:
	// MultiWriter 遇到首个失败即中止,stderr 异常(如管道被关)不应中断落盘,
	// 而文件写入器自身从不返回错误。
	out := io.Writer(w)
	if _, err := os.Stderr.Stat(); err == nil {
		out = io.MultiWriter(w, os.Stderr)
	}
	log.SetOutput(out)
	return dir, nil
}

// FlushLogs 把缓冲中的日志立即落盘;进程退出前调用。未启用文件日志时为空操作。
func FlushLogs() {
	fileLogMu.Lock()
	w := fileLogWriter
	fileLogMu.Unlock()
	if w != nil {
		w.Flush()
	}
}

// Fatalf 记录致命错误、把日志强制落盘后以退出码 1 终止进程。
// 入口点必须用它替代 log.Fatalf:log.Fatalf 内部 os.Exit 既不走 defer 也不 flush,
// 在 windowsgui 桌面构建下(无有效 stderr、日志只写文件)会让缓冲中的致命信息随
// 进程退出而丢失,启动失败(端口占用 / WebView2 缺失等)静默无痕。
func Fatalf(format string, args ...any) {
	log.Printf("[FATAL] "+format, args...)
	FlushLogs()
	os.Exit(1)
}

// pruneOldLogs 删除 dir 下文件名日期早于保留期的 sniffy-*.log。
func pruneOldLogs(dir string, keepDays int) {
	pruneOldLogsPrefixed(dir, logFilePrefix, keepDays)
}

// pruneOldLogsPrefixed 删除 dir 下以 prefix 开头、文件名日期早于保留期的 <prefix><日期>.log。
func pruneOldLogsPrefixed(dir, prefix string, keepDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, logFileSuffix) {
			continue
		}
		dayStr := strings.TrimSuffix(strings.TrimPrefix(name, prefix), logFileSuffix)
		day, err := time.ParseInLocation(logDayFormat, dayStr, time.Local)
		if err != nil {
			continue
		}
		if day.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
