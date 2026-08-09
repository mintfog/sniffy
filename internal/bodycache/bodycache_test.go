// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package bodycache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewClearsLeftovers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	stale := filepath.Join(dir, "old.body")
	if err := os.WriteFile(stale, []byte("上次运行留下的"), 0o600); err != nil {
		t.Fatalf("写残留文件失败: %v", err)
	}

	if _, err := New(dir, 0); err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("启动时应清空上次运行的残留副本,但文件仍在: %v", err)
	}
}

func TestCreateCommitAndRead(t *testing.T) {
	c, err := New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := c.Create("abc123")
	want := []byte("视频分片字节")
	if _, err := e.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	path, size := e.Commit()
	if path == "" {
		t.Fatal("Commit 应返回副本路径")
	}
	if size != int64(len(want)) {
		t.Fatalf("字节数不对: want %d, got %d", len(want), size)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读副本失败: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("副本内容不一致: want %q, got %q", want, got)
	}
	if c.Total() != size {
		t.Fatalf("记账不对: want %d, got %d", size, c.Total())
	}
}

func TestAbortRemovesFile(t *testing.T) {
	c, err := New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := c.Create("abort-me")
	_, _ = e.Write([]byte("半截"))
	path := e.path
	e.Abort()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Abort 后副本应被删除: %v", err)
	}
	if p, n := e.Commit(); p != "" || n != 0 {
		t.Fatalf("Abort 后 Commit 应返回空: %q %d", p, n)
	}
	if c.Total() != 0 {
		t.Fatalf("Abort 不应计入容量: got %d", c.Total())
	}
}

// TestBudgetEvictsOldest 锁定容量上限:超出后从最旧的副本开始删,总量回到上限内。
func TestBudgetEvictsOldest(t *testing.T) {
	c, err := New(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	paths := make([]string, 0, 3)
	for _, id := range []string{"a", "b", "c"} {
		e := c.Create(id)
		_, _ = e.Write([]byte("12345")) // 每份 5 字节
		p, _ := e.Commit()
		paths = append(paths, p)
	}
	if c.Total() != 10 {
		t.Fatalf("总量应回到上限内: want 10, got %d", c.Total())
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("最旧的副本应被淘汰: %v", err)
	}
	for _, p := range paths[1:] {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("较新的副本不应被淘汰: %v", err)
		}
	}
}

func TestRemoveAndClear(t *testing.T) {
	c, err := New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := c.Create("gone")
	_, _ = e.Write([]byte("x"))
	path, _ := e.Commit()

	c.Remove(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Remove 后文件应消失: %v", err)
	}
	if c.Total() != 0 {
		t.Fatalf("Remove 后记账应清零: got %d", c.Total())
	}

	e2 := c.Create("bye")
	_, _ = e2.Write([]byte("y"))
	p2, _ := e2.Commit()
	c.Clear()
	if _, err := os.Stat(p2); !os.IsNotExist(err) {
		t.Fatalf("Clear 后文件应消失: %v", err)
	}
	if c.Total() != 0 {
		t.Fatalf("Clear 后记账应清零: got %d", c.Total())
	}
}

// TestNilCacheIsNoop 锁定未启用缓存时的空操作行为:调用方不必到处判空。
func TestNilCacheIsNoop(t *testing.T) {
	var c *Cache
	e := c.Create("x")
	if _, err := e.Write([]byte("数据")); err != nil {
		t.Fatalf("nil Entry 的 Write 不应报错: %v", err)
	}
	if p, n := e.Commit(); p != "" || n != 0 {
		t.Fatalf("nil Entry 的 Commit 应返回空: %q %d", p, n)
	}
	e.Abort()
	c.Remove("/tmp/whatever")
	c.Clear()
	if c.Dir() != "" || c.Total() != 0 {
		t.Fatal("nil Cache 的查询应返回零值")
	}
}
