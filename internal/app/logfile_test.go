// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// failNWriter 前 failsLeft 次 Write 返回错误,之后成功。模拟瞬时磁盘故障。
type failNWriter struct{ failsLeft int }

func (f *failNWriter) Write(p []byte) (int, error) {
	if f.failsLeft > 0 {
		f.failsLeft--
		return 0, errors.New("transient io error")
	}
	return len(p), nil
}

// newTestWriter 构造一个使用可控时钟的写入器。
func newTestWriter(t *testing.T) (*rotatingFileWriter, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	clock := time.Date(2026, 6, 10, 12, 0, 0, 0, time.Local)
	w := newRotatingFileWriter(dir)
	w.now = func() time.Time { return clock }
	t.Cleanup(w.Close) // Windows 下不关闭句柄会导致 TempDir 清理失败
	return w, &clock
}

func logFileContent(t *testing.T, dir, day string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, logFilePrefix+day+logFileSuffix))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestIdleWriteThrough(t *testing.T) {
	w, _ := newTestWriter(t)
	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// 低频写入应直写落盘,无需 Flush 即可读到。
	if got := logFileContent(t, w.dir, "2026-06-10"); got != "hello\n" {
		t.Fatalf("低频写入未直写落盘, got %q", got)
	}
}

func TestBurstBufferedThenDelayedFlush(t *testing.T) {
	w, clock := newTestWriter(t)
	w.flushDelay = 20 * time.Millisecond

	_, _ = w.Write([]byte("first\n")) // 直写(idle)
	*clock = clock.Add(time.Millisecond)
	_, _ = w.Write([]byte("second\n")) // 突发,进缓冲

	if got := logFileContent(t, w.dir, "2026-06-10"); strings.Contains(got, "second") {
		t.Fatalf("突发写入不应立即落盘, got %q", got)
	}
	// 等延迟落盘定时器触发。
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := logFileContent(t, w.dir, "2026-06-10"); strings.Contains(got, "second") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("延迟落盘未发生")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestErrorLineWriteThrough(t *testing.T) {
	w, clock := newTestWriter(t)
	_, _ = w.Write([]byte("first\n"))
	*clock = clock.Add(time.Millisecond)
	_, _ = w.Write([]byte("[ERROR] boom\n")) // 突发中的错误行也应直写
	if got := logFileContent(t, w.dir, "2026-06-10"); !strings.Contains(got, "[ERROR] boom") {
		t.Fatalf("错误日志未直写落盘, got %q", got)
	}
}

func TestRotateByDay(t *testing.T) {
	w, clock := newTestWriter(t)
	_, _ = w.Write([]byte("day1\n"))
	*clock = clock.Add(24 * time.Hour)
	_, _ = w.Write([]byte("day2\n"))
	w.Flush()

	if got := logFileContent(t, w.dir, "2026-06-10"); got != "day1\n" {
		t.Fatalf("第一天文件内容错误: %q", got)
	}
	if got := logFileContent(t, w.dir, "2026-06-11"); got != "day2\n" {
		t.Fatalf("第二天文件内容错误: %q", got)
	}
}

func TestDailyCap(t *testing.T) {
	w, clock := newTestWriter(t)
	w.maxDaily = 32

	_, _ = w.Write([]byte("0123456789\n")) // 11 字节
	*clock = clock.Add(time.Millisecond)
	_, _ = w.Write([]byte("0123456789\n")) // 22 字节
	*clock = clock.Add(time.Millisecond)
	_, _ = w.Write([]byte("0123456789\n")) // 33 字节 > 32,触发上限
	*clock = clock.Add(time.Millisecond)
	_, _ = w.Write([]byte("dropped\n")) // 应被丢弃
	w.Flush()

	got := logFileContent(t, w.dir, "2026-06-10")
	if !strings.Contains(got, "体积上限") {
		t.Fatalf("未写入上限标记: %q", got)
	}
	if strings.Contains(got, "dropped") {
		t.Fatalf("超限日志未被丢弃: %q", got)
	}

	// 跨天后恢复写入。
	*clock = clock.Add(24 * time.Hour)
	_, _ = w.Write([]byte("newday\n"))
	w.Flush()
	if got := logFileContent(t, w.dir, "2026-06-11"); !strings.Contains(got, "newday") {
		t.Fatalf("跨天后未恢复写入: %q", got)
	}
}

func TestCapSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	day := time.Now().Format(logDayFormat)
	// 预置一个已超限的当日文件,模拟进程重启。
	big := strings.Repeat("x", 64)
	if err := os.WriteFile(filepath.Join(dir, logFilePrefix+day+logFileSuffix), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	w := newRotatingFileWriter(dir)
	t.Cleanup(w.Close)
	w.maxDaily = 32
	_, _ = w.Write([]byte("dropped\n"))
	w.Flush()
	if got := logFileContent(t, dir, day); strings.Contains(got, "dropped") {
		t.Fatalf("重启后体积上限被绕过: %q", got)
	}
}

func TestOpenFailureBackoff(t *testing.T) {
	w, clock := newTestWriter(t)
	w.dir = filepath.Join(w.dir, "missing", "nested") // 不存在且无法直接创建文件
	if _, err := w.Write([]byte("a\n")); err != nil {
		t.Fatalf("打开失败不应向调用方返回错误: %v", err)
	}
	if w.file != nil {
		t.Fatal("打开失败后 file 应为 nil")
	}
	// 退避期内不重试(reopenAfter 在未来)。
	if !w.reopenAfter.After(*clock) {
		t.Fatal("未进入退避期")
	}
}

func TestPruneOldLogs(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -10).Format(logDayFormat)
	recent := time.Now().Format(logDayFormat)
	for _, name := range []string{
		logFilePrefix + old + logFileSuffix,
		logFilePrefix + recent + logFileSuffix,
		"unrelated.txt",
		logFilePrefix + "not-a-date" + logFileSuffix,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pruneOldLogs(dir, logKeepDays)

	if _, err := os.Stat(filepath.Join(dir, logFilePrefix+old+logFileSuffix)); !os.IsNotExist(err) {
		t.Fatal("过期日志未被清理")
	}
	for _, name := range []string{
		logFilePrefix + recent + logFileSuffix,
		"unrelated.txt",
		logFilePrefix + "not-a-date" + logFileSuffix,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("不应清理 %s: %v", name, err)
		}
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	w := newRotatingFileWriter(dir)
	t.Cleanup(w.Close)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				_, _ = w.Write([]byte("concurrent line for race detection\n"))
			}
		})
	}
	wg.Wait()
	w.Flush()
	got := logFileContent(t, dir, time.Now().Format(logDayFormat))
	if lines := strings.Count(got, "\n"); lines != 8*200 {
		t.Fatalf("并发写入丢行: got %d lines, want %d", lines, 8*200)
	}
}

func TestRecoversAfterTransientWriteError(t *testing.T) {
	w, clock := newTestWriter(t)

	// 正常写一条,完成首次 rotate(真实文件)。
	_, _ = w.Write([]byte("boot\n"))

	// 把底层换成会失败一次的 writer:模拟瞬时故障 + bufio 粘性错误。
	w.buf = bufio.NewWriterSize(&failNWriter{failsLeft: 1}, logBufSize)

	*clock = clock.Add(time.Millisecond)
	_, _ = w.Write([]byte("[ERROR] during outage\n")) // 触发内联 flush → 失败 → fail()
	if w.file != nil {
		t.Fatal("写失败后应丢弃句柄(file=nil),以便下次重开")
	}

	// 退避期内不重开:写入被丢弃,不反复 syscall。
	_, _ = w.Write([]byte("still in backoff\n"))
	if w.file != nil {
		t.Fatal("退避期内不应重开文件")
	}

	// 越过退避期后写入应重开真实文件并落盘(从瞬时故障中恢复)。
	*clock = clock.Add(2 * time.Second)
	_, _ = w.Write([]byte("[ERROR] recovered\n"))
	w.Flush()

	got := logFileContent(t, w.dir, "2026-06-10")
	if !strings.Contains(got, "boot") {
		t.Fatalf("故障前日志丢失: %q", got)
	}
	if !strings.Contains(got, "recovered") {
		t.Fatalf("故障恢复后日志未落盘(说明被粘性错误黑洞): %q", got)
	}
}

func TestCapDurableAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	day := time.Now().Format(logDayFormat)

	w1 := newRotatingFileWriter(dir)
	w1.maxDaily = 40
	for range 4 {
		_, _ = w1.Write([]byte("0123456789\n")) // 每条 11 字节,第 4 条触发封顶
	}
	w1.Flush()
	w1.Close()

	fi, err := os.Stat(filepath.Join(dir, logFilePrefix+day+logFileSuffix))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= 40 {
		t.Fatalf("封顶后文件应真正超过上限(含越界行),实际 %d 字节", fi.Size())
	}

	// 重启:新 writer 读到的文件已超限,应保持封顶,不解封、不重复写标记。
	w2 := newRotatingFileWriter(dir)
	t.Cleanup(w2.Close)
	w2.maxDaily = 40
	_, _ = w2.Write([]byte("after restart\n"))
	w2.Flush()

	got := logFileContent(t, dir, day)
	if strings.Contains(got, "after restart") {
		t.Fatalf("重启后封顶被解除,继续写入: %q", got)
	}
	if n := strings.Count(got, "体积上限"); n != 1 {
		t.Fatalf("封顶标记应恰好 1 条,实际 %d 条", n)
	}
}

// BenchmarkBurstWrite 模拟高频突发写入(缓冲路径)。
func BenchmarkBurstWrite(b *testing.B) {
	dir := b.TempDir()
	w := newRotatingFileWriter(dir)
	b.Cleanup(w.Close)
	line := []byte("2026/06/10 12:00:00 [INFO] New connection from 192.168.1.100:54321\n")
	b.ReportAllocs()
	for b.Loop() {
		_, _ = w.Write(line)
	}
}

// BenchmarkDirectWrite 对照组:每条日志一次同步磁盘写(旧实现的行为)。
func BenchmarkDirectWrite(b *testing.B) {
	dir := b.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "direct.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	line := []byte("2026/06/10 12:00:00 [INFO] New connection from 192.168.1.100:54321\n")
	b.ReportAllocs()
	for b.Loop() {
		_, _ = f.Write(line)
	}
}
