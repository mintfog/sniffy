// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func specs(urls ...string) []BreakRuleSpec {
	out := make([]BreakRuleSpec, 0, len(urls))
	for i, u := range urls {
		out = append(out, BreakRuleSpec{
			ID:        "bp-" + u,
			URL:       u,
			OnRequest: true,
			Enabled:   i%2 == 0,
		})
	}
	return out
}

// 落盘后重建应原样回来,含顺序与启用状态 —— 规则是用户逐条敲进去的,顺序也是信息。
func TestBreakRuleStorePersistenceRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), breakRuleFileName)
	s := newBreakRuleStore(path)
	if err := s.save(specs("https://a.example/*", "https://b.example/*")); err != nil {
		t.Fatalf("落盘失败: %v", err)
	}

	got := newBreakRuleStore(path).list()
	if len(got) != 2 {
		t.Fatalf("重载条数 = %d, want 2", len(got))
	}
	if got[0].URL != "https://a.example/*" || got[1].URL != "https://b.example/*" {
		t.Errorf("重载顺序错乱: %+v", got)
	}
	if !got[0].Enabled || got[1].Enabled {
		t.Errorf("启用状态未原样回来: %+v", got)
	}

	// 删除同样是整体覆盖,否则重启后规则复活。
	if err := s.save(specs("https://a.example/*")); err != nil {
		t.Fatalf("覆盖失败: %v", err)
	}
	if n := len(newBreakRuleStore(path).list()); n != 1 {
		t.Fatalf("覆盖应落盘,重载后剩 %d 条", n)
	}
}

// 规则文件被外部编辑坏掉时按空集起步,不阻塞启动,且能被新内容盖掉。
func TestBreakRuleStoreSurvivesBrokenFile(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, content string }{
		{"非法 JSON", "{ 这不是 JSON"},
		{"类型不匹配", `{"rules": []}`},
		{"空文件", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), breakRuleFileName)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("写入用例文件失败: %v", err)
			}
			s := newBreakRuleStore(path)
			if n := len(s.list()); n != 0 {
				t.Fatalf("坏文件应按空集起步, got %d", n)
			}
			if err := s.save(specs("https://c.example/*")); err != nil {
				t.Fatalf("坏文件之后仍应可写: %v", err)
			}
			if n := len(newBreakRuleStore(path).list()); n != 1 {
				t.Fatalf("新内容应覆盖坏文件, got %d", n)
			}
		})
	}
}

// 路径为空(纯内存装配)时读写都不该炸。
func TestBreakRuleStoreInMemory(t *testing.T) {
	t.Parallel()
	s := newBreakRuleStore("")
	if err := s.save(specs("https://d.example/*")); err != nil {
		t.Fatalf("纯内存保存不应失败: %v", err)
	}
	if n := len(s.list()); n != 1 {
		t.Fatalf("纯内存也应能读回, got %d", n)
	}
}

func TestBreakRuleStoreFilePermission(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不按 POSIX 权限位")
	}
	path := filepath.Join(t.TempDir(), breakRuleFileName)
	if err := newBreakRuleStore(path).save(specs("https://e.example/*")); err != nil {
		t.Fatalf("落盘失败: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("落盘文件应存在: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("断点规则文件权限应为 0600,得到 %o", perm)
	}
}
