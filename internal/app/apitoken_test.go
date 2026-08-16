// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsureAPITokenFile(t *testing.T) {
	dir := t.TempDir()

	token, path, err := ensureAPITokenFile(dir)
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("token 长度 = %d,期望 64(32 字节 hex)", len(token))
	}
	if path != filepath.Join(dir, apiTokenFileName) {
		t.Fatalf("token 路径 = %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("token 文件未落盘: %v", err)
	}
	assertPerm(t, path, 0o600)

	again, _, err := ensureAPITokenFile(dir)
	if err != nil {
		t.Fatalf("二次调用失败: %v", err)
	}
	if again != token {
		t.Fatalf("二次调用生成了新 token: %q != %q", again, token)
	}
}

func TestEnsureTokenSecrecyRotatesWidePerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, apiTokenFileName)
	if err := os.WriteFile(path, []byte("leaked-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	token, rotated, err := ensureTokenSecrecy(dir)
	if err != nil {
		t.Fatalf("处理已有文件失败: %v", err)
	}
	if runtime.GOOS == "windows" {
		if token != "leaked-token" || rotated {
			t.Fatalf("Windows 上无权限判定应复用原值不轮换,got token=%q rotated=%v", token, rotated)
		}
		return
	}
	if !rotated {
		t.Fatal("0644 文件应触发轮换")
	}
	if token == "leaked-token" || token == "" {
		t.Fatalf("过宽文件应被轮换为新值,got %q", token)
	}
	if onDisk := loadAPITokenFile(dir); onDisk != token {
		t.Fatalf("磁盘 token = %q,应与轮换后的新值 %q 一致", onDisk, token)
	}
	assertPerm(t, path, 0o600)
}

func TestEnsureTokenSecrecyReuses0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, apiTokenFileName)
	if err := os.WriteFile(path, []byte("good-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, rotated, err := ensureTokenSecrecy(dir)
	if err != nil {
		t.Fatalf("复用失败: %v", err)
	}
	if rotated {
		t.Fatal("0600 文件不应轮换")
	}
	if token != "good-token" {
		t.Fatalf("token = %q,期望复用 0600 文件原值", token)
	}
}

func TestEnsureTokenSecrecyConcurrentRotation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位,轮换按权限判定不适用")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, apiTokenFileName)
	if err := os.WriteFile(path, []byte("leaked-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const n = 8
	tokens := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			tokens[i], _, errs[i] = ensureTokenSecrecy(dir)
		}(i)
	}
	wg.Wait()

	fileTok := loadAPITokenFile(dir)
	if fileTok == "" || fileTok == "leaked-token" {
		t.Fatalf("轮换后磁盘 token = %q,应为全新值", fileTok)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d 出错: %v", i, errs[i])
		}
		if tokens[i] == "leaked-token" {
			t.Fatalf("goroutine %d 仍返回已泄漏的旧 token(轮换未协调)", i)
		}
		if tokens[i] != fileTok {
			t.Fatalf("goroutine %d 拿到 %q,与文件最终值 %q 不一致", i, tokens[i], fileTok)
		}
	}
	assertPerm(t, path, 0o600)
}

func TestSecureFilePerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, apiTokenFileName)
	if err := os.WriteFile(path, []byte("live-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := secureFilePerm(path); err != nil {
		t.Fatalf("收紧失败: %v", err)
	}
	assertPerm(t, path, 0o600)

	if err := secureFilePerm(filepath.Join(dir, "does-not-exist")); err != nil {
		t.Fatalf("不存在文件应返回 nil,got %v", err)
	}
}

func TestEnsureAPITokenConcurrentPublish(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	tokens := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			tokens[i], _, errs[i] = ensureAPITokenFile(dir)
		}(i)
	}
	wg.Wait()

	fileTok := loadAPITokenFile(dir)
	if fileTok == "" {
		t.Fatal("并发生成后文件为空")
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d 出错: %v", i, errs[i])
		}
		if tokens[i] != fileTok {
			t.Fatalf("goroutine %d 拿到 %q,与文件 %q 不一致(发生互相覆盖)", i, tokens[i], fileTok)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("并发发布后残留文件数 = %d,期望 1", len(entries))
	}
}

func TestEnsureAPITokenAtomicPublish(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ensureAPITokenFile(dir); err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != apiTokenFileName {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("目录内容 = %v,期望仅 %s(无 .tmp 残留)", names, apiTokenFileName)
	}
	assertPerm(t, filepath.Join(dir, apiTokenFileName), 0o600)
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != want {
		t.Fatalf("%s 权限 = %o,期望 %o", path, perm, want)
	}
}

func TestLoadAPITokenFileTrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, apiTokenFileName), []byte("  tok-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadAPITokenFile(dir); got != "tok-123" {
		t.Fatalf("loadAPITokenFile = %q,期望 tok-123", got)
	}
}

func TestAPITokenEnvOverride(t *testing.T) {
	t.Setenv(apiTokenEnv, "from-env")
	if got := LoadAPIToken(); got != "from-env" {
		t.Fatalf("LoadAPIToken = %q,期望环境变量优先", got)
	}
	token, path, err := EnsureAPIToken()
	if err != nil {
		t.Fatalf("EnsureAPIToken 失败: %v", err)
	}
	if token != "from-env" || path != "" {
		t.Fatalf("EnsureAPIToken = (%q, %q),期望环境变量 token 且不落盘", token, path)
	}
}

func TestEnsureTokenSecrecySkipsWithEnv(t *testing.T) {
	t.Setenv(apiTokenEnv, "from-env")
	token, rotated, err := EnsureTokenSecrecy()
	if err != nil || token != "" || rotated {
		t.Fatalf("环境变量凭证下应 (\"\", false, nil),got (%q, %v, %v)", token, rotated, err)
	}
}

func TestRotateIfWide(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位,轮换按权限判定不适用")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, apiTokenFileName)

	if err := os.WriteFile(path, []byte("leaked-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newTok, rotated, err := rotateIfWide(dir, path)
	if err != nil {
		t.Fatalf("轮换失败: %v", err)
	}
	if !rotated {
		t.Fatal("0644 文件应触发轮换")
	}
	if newTok == "" || newTok == "leaked-token" {
		t.Fatalf("轮换后 token = %q,应为全新值", newTok)
	}
	if onDisk := loadAPITokenFile(dir); onDisk != newTok {
		t.Fatalf("磁盘 token = %q,应与返回的新值 %q 一致", onDisk, newTok)
	}
	assertPerm(t, path, 0o600)

	_, rotated, err = rotateIfWide(dir, path)
	if err != nil {
		t.Fatalf("二次轮换出错: %v", err)
	}
	if rotated {
		t.Fatal("0600 文件不应轮换")
	}

	_, rotated, err = rotateIfWide(dir, filepath.Join(dir, "nope"))
	if err != nil || rotated {
		t.Fatalf("不存在文件应 rotated=false err=nil,got rotated=%v err=%v", rotated, err)
	}
}

func TestWithTokenDirLockMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	var inside int32
	var violations int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = withTokenDirLock(dir, func() error {
				if atomic.AddInt32(&inside, 1) != 1 {
					atomic.AddInt32(&violations, 1)
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&inside, -1)
				return nil
			})
		}()
	}
	wg.Wait()
	if violations != 0 {
		t.Fatalf("检测到 %d 次临界区重叠,flock 未互斥", violations)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"::1", true},
		{"localhost", true},
		{"0.0.0.0", false},
		{"::", false},
		{"", false},
		{"192.168.1.10", false},
		{"example.com", false},
	}
	for _, c := range cases {
		if got := IsLoopbackHost(c.host); got != c.want {
			t.Errorf("IsLoopbackHost(%q) = %v,期望 %v", c.host, got, c.want)
		}
	}
}
