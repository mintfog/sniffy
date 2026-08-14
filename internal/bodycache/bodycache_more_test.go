// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package bodycache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// requireUnixPerm 跳过依赖 POSIX 权限位的用例:Windows 不按 mode 位拒绝访问,root 则无视权限位。
func requireUnixPerm(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不按 Unix 权限位拒绝目录访问")
	}
	if os.Geteuid() == 0 {
		t.Skip("root 无视权限位")
	}
}

// commitBody 写入并提交一份副本,返回路径。
func commitBody(t *testing.T, c *Cache, flowID string, data []byte) string {
	t.Helper()
	e := c.Create(flowID)
	if e == nil {
		t.Fatalf("Create(%q) 返回 nil", flowID)
	}
	if _, err := e.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	path, n := e.Commit()
	if path == "" || n != int64(len(data)) {
		t.Fatalf("Commit(%q) 应返回路径与字节数: %q %d", flowID, path, n)
	}
	return path
}

func TestNewFailsWhenDirIsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(path, []byte("我不是目录"), 0o600); err != nil {
		t.Fatalf("写占位文件失败: %v", err)
	}
	c, err := New(path, 0)
	if err == nil {
		t.Fatal("缓存目录被普通文件占用时 New 应报错")
	}
	if c != nil {
		t.Fatalf("New 出错时不应返回 Cache: %+v", c)
	}
}

func TestNewFailsWhenDirUnreadable(t *testing.T) {
	requireUnixPerm(t)
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	// 去掉读权限:MkdirAll 只 Stat 因而仍成功,随后的 ReadDir 会失败。
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatalf("改权限失败: %v", err)
	}
	// 不恢复权限的话 t.TempDir 的清理会失败。
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := New(dir, 0); err == nil {
		t.Fatal("缓存目录不可读时 New 应报错")
	}
}

// TestNewKeepsSubdirectories 锁定清理范围:只删残留的文件,子目录留着(它们不是副本)。
func TestNewKeepsSubdirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("建子目录失败: %v", err)
	}
	leftover := filepath.Join(dir, "left.body")
	if err := os.WriteFile(leftover, []byte("残留"), 0o600); err != nil {
		t.Fatalf("写残留文件失败: %v", err)
	}

	c, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := os.Stat(sub); err != nil {
		t.Fatalf("子目录不应被删除: %v", err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("残留副本应被删除: %v", err)
	}
	if c.Dir() != dir {
		t.Fatalf("Dir 应返回缓存目录: want %q, got %q", dir, c.Dir())
	}
}

// TestNewDefaultsBudget 锁定 budget<=0 取默认值:提交一份小副本不会被立刻淘汰。
func TestNewDefaultsBudget(t *testing.T) {
	c, err := New(t.TempDir(), -1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.budget != DefaultBudget {
		t.Fatalf("budget<=0 应取默认值: want %d, got %d", DefaultBudget, c.budget)
	}
	path := commitBody(t, c, "small", []byte("一点点"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("默认容量下副本不应被淘汰: %v", err)
	}
}

// TestSetBudgetShrinkEvicts 锁定收缩容量会立即按最旧顺序淘汰,直到总量回到新上限内,
// 且被淘汰的文件真的从盘上删掉——记账摘除后它们不会再被任何路径淘汰,漏删即永久泄漏。
func TestSetBudgetShrinkEvicts(t *testing.T) {
	c, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	paths := []string{
		commitBody(t, c, "a", []byte("12345")),
		commitBody(t, c, "b", []byte("12345")),
		commitBody(t, c, "c", []byte("12345")),
	}
	if c.Total() != 15 {
		t.Fatalf("提交后总量不对: want 15, got %d", c.Total())
	}

	c.SetBudget(10)
	if c.Total() != 10 {
		t.Fatalf("收缩容量后总量应回到上限内: want 10, got %d", c.Total())
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("最旧的副本应已从盘上删除: err=%v", err)
	}
	for _, p := range paths[1:] {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("上限内的副本不应被删除: %v", err)
		}
	}
	// 淘汰的是最旧的一份:它已从记账里摘除,再 Remove 也不会让总量变化。
	c.Remove(paths[0])
	if c.Total() != 10 {
		t.Fatalf("被淘汰的副本不应重复记账: got %d", c.Total())
	}
	// 较新的两份仍在记账中,逐个 Remove 能把总量清零。
	c.Remove(paths[1])
	c.Remove(paths[2])
	if c.Total() != 0 {
		t.Fatalf("移除全部副本后总量应清零: got %d", c.Total())
	}
}

// TestSetBudgetIgnoresNonPositive 锁定非正数被忽略:若真被写入 budget,已有副本会被全部淘汰。
func TestSetBudgetIgnoresNonPositive(t *testing.T) {
	c, err := New(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	commitBody(t, c, "keep", []byte("12345"))

	c.SetBudget(0)
	c.SetBudget(-100)
	if c.Total() != 5 {
		t.Fatalf("非正数上限应被忽略: want 5, got %d", c.Total())
	}
	if c.budget != 10 {
		t.Fatalf("非正数不应改写上限: want 10, got %d", c.budget)
	}

	var nilCache *Cache
	nilCache.SetBudget(1) // 未启用缓存时应为空操作
}

func TestCreateReturnsNilOnFailure(t *testing.T) {
	c, err := New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e := c.Create(""); e != nil {
		t.Fatal("空 flowID 不应建副本")
	}
	// 目标落在不存在的子目录里,OpenFile 必定失败。
	e := c.Create(filepath.Join("missing", "child"))
	if e != nil {
		t.Fatal("建文件失败时 Create 应返回 nil")
	}
	// 返回 nil 是契约的一部分:调用方直接接着用,不判空。
	payload := []byte("数据")
	if n, err := e.Write(payload); n != len(payload) || err != nil {
		t.Fatalf("nil Entry 的 Write 应吞掉写入: %d %v", n, err)
	}
	if p, n := e.Commit(); p != "" || n != 0 {
		t.Fatalf("nil Entry 的 Commit 应返回空: %q %d", p, n)
	}
	e.Abort()
	if c.Total() != 0 {
		t.Fatalf("失败的 Create 不应计入容量: got %d", c.Total())
	}
}

// TestCommitSamePathTwiceReplacesAccounting 锁定同一 flowID 重开副本时的记账:
// 覆盖旧记录而不是叠加,顺序表里也只保留一条。
func TestCommitSamePathTwiceReplacesAccounting(t *testing.T) {
	c, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first := commitBody(t, c, "dup", []byte("12345"))
	second := commitBody(t, c, "dup", []byte("678"))
	if first != second {
		t.Fatalf("同一 flowID 应落在同一路径: %q vs %q", first, second)
	}
	if c.Total() != 3 {
		t.Fatalf("重复提交应覆盖旧记账: want 3, got %d", c.Total())
	}
	if got, err := os.ReadFile(second); err != nil || string(got) != "678" {
		t.Fatalf("重开副本应截断旧内容: %q %v", got, err)
	}

	c.Remove(second)
	if c.Total() != 0 {
		t.Fatalf("顺序表里不应有重复项: got %d", c.Total())
	}
}

// TestWriteFailureDropsCopy 模拟落盘失败(磁盘满 / 权限):提前关掉底层文件让后续 Write 出错。
// Write 仍必须返回 (len(p), nil),否则挂在 MultiWriter 上会连带中断转发给客户端的那一路。
func TestWriteFailureDropsCopy(t *testing.T) {
	c, err := New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := c.Create("broken")
	if _, err := e.Write([]byte("前半段")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := e.f.Close(); err != nil {
		t.Fatalf("关闭底层文件失败: %v", err)
	}

	payload := []byte("后半段")
	n, err := e.Write(payload)
	if n != len(payload) || err != nil {
		t.Fatalf("落盘失败时 Write 也要报告全部写入: %d %v", n, err)
	}
	if !e.failed {
		t.Fatal("落盘失败后应标记 failed")
	}
	if _, err := os.Stat(e.path); !os.IsNotExist(err) {
		t.Fatalf("落盘失败后残缺副本应被删除: %v", err)
	}
	// 失败后的写入被静默吞掉,不会重建文件。
	if n, err := e.Write(payload); n != len(payload) || err != nil {
		t.Fatalf("失败后 Write 仍应吞掉写入: %d %v", n, err)
	}
	if _, err := os.Stat(e.path); !os.IsNotExist(err) {
		t.Fatalf("失败后的写入不应重建副本: %v", err)
	}
	if p, size := e.Commit(); p != "" || size != 0 {
		t.Fatalf("失败的副本 Commit 应返回空: %q %d", p, size)
	}
	if c.Total() != 0 {
		t.Fatalf("失败的副本不应计入容量: got %d", c.Total())
	}
}

// TestCommitTwiceReturnsEmpty 锁定重复 Commit:第二次 Close 必然出错,此时按写入失败处理
// —— 返回空路径并删掉副本,调用方拿到的空路径意味着「没有副本可读」。
func TestCommitTwiceReturnsEmpty(t *testing.T) {
	c, err := New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := c.Create("twice")
	if _, err := e.Write([]byte("12345")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	path, size := e.Commit()
	if path == "" || size != 5 {
		t.Fatalf("首次 Commit 应成功: %q %d", path, size)
	}

	p2, n2 := e.Commit()
	if p2 != "" || n2 != 0 {
		t.Fatalf("重复 Commit 应返回空: %q %d", p2, n2)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Close 出错时应删掉副本: %v", err)
	}
}

// TestRemoveUnknownPathKeepsAccounting 锁定 Remove 不认识的路径时不动记账(只尝试删文件)。
func TestRemoveUnknownPathKeepsAccounting(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	commitBody(t, c, "tracked", []byte("12345"))

	c.Remove(filepath.Join(dir, "never-committed.body"))
	c.Remove("")
	if c.Total() != 5 {
		t.Fatalf("未记账的路径不应影响总量: want 5, got %d", c.Total())
	}
}
