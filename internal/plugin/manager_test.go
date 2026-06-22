// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mintfog/sniffy/internal/pipeline"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	pipe := pipeline.New(nil, nil)
	return NewManager(pipe, t.TempDir(), nil, nil)
}

func find(list []map[string]any, id string) map[string]any {
	for _, m := range list {
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func TestCreateAndDelete(t *testing.T) {
	m := newTestManager(t)
	meta := map[string]any{"id": "my-plugin", "name": "Mine", "priority": float64(50)}
	created, err := m.CreatePlugin(meta, "function onRequest(f){}")
	if err != nil {
		t.Fatalf("CreatePlugin: %v", err)
	}
	if created["id"] != "my-plugin" {
		t.Fatalf("created id = %v", created["id"])
	}
	if find(m.ListPlugins(), "my-plugin") == nil {
		t.Fatal("plugin not listed after create")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "my-plugin", "index.js")); err != nil {
		t.Fatalf("entry not written: %v", err)
	}

	if err := m.DeletePlugin("my-plugin"); err != nil {
		t.Fatalf("DeletePlugin: %v", err)
	}
	if find(m.ListPlugins(), "my-plugin") != nil {
		t.Fatal("plugin still listed after delete")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "my-plugin")); !os.IsNotExist(err) {
		t.Fatal("plugin dir not removed")
	}
}

func TestCreateRejectsBadID(t *testing.T) {
	m := newTestManager(t)
	for _, bad := range []string{"", "../escape", "Has Space", "UPPER"} {
		if _, err := m.CreatePlugin(map[string]any{"id": bad}, ""); err == nil {
			t.Fatalf("expected error for id %q", bad)
		}
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.CreatePlugin(map[string]any{"id": "dup"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePlugin(map[string]any{"id": "dup"}, ""); err == nil {
		t.Fatal("expected duplicate-id error")
	}
}

// 保存非法源码必须原子失败:磁盘上的旧源码与运行中的旧实例都不受影响。
func TestSaveSourceAtomicOnError(t *testing.T) {
	m := newTestManager(t)
	good := "function onRequest(f){}"
	if _, err := m.CreatePlugin(map[string]any{"id": "atom"}, good); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(m.dir, "atom", "index.js")

	if err := m.SavePluginSource("atom", "function onRequest(f){ this is not valid js"); err == nil {
		t.Fatal("expected compile error on bad source")
	}
	data, _ := os.ReadFile(entry)
	if string(data) != good {
		t.Fatalf("disk source corrupted by failed save: %q", data)
	}
	src, ok := m.GetPluginSource("atom")
	if !ok || src != good {
		t.Fatalf("live plugin source changed: ok=%v src=%q", ok, src)
	}
}

// 加载失败的插件目录应在 ListPlugins 中带 error 字段出现,而非凭空消失。
func TestFailedLoadSurfaced(t *testing.T) {
	m := newTestManager(t)
	dir := filepath.Join(m.dir, "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"broken","entry":"index.js"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "index.js"), []byte("syntax ((("), 0o644)

	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	entry := find(m.ListPlugins(), "broken")
	if entry == nil {
		t.Fatal("failed plugin not surfaced")
	}
	if entry["error"] == nil || entry["error"] == "" {
		t.Fatalf("failed plugin missing error field: %+v", entry)
	}
}

// 并发创建同一 id:opMu 串行化后应只有一个成功,且 map 中只剩一个实例(不泄漏)。
func TestConcurrentCreateSameID(t *testing.T) {
	m := newTestManager(t)
	var wg sync.WaitGroup
	var okCount int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.CreatePlugin(map[string]any{"id": "dup"}, "function onRequest(f){}"); err == nil {
				atomic.AddInt32(&okCount, 1)
			}
		}()
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("expected exactly 1 successful create, got %d", okCount)
	}
	cnt := 0
	for _, p := range m.ListPlugins() {
		if p["id"] == "dup" {
			cnt++
		}
	}
	if cnt != 1 {
		t.Fatalf("expected 1 dup entry, got %d", cnt)
	}
}

// 并发保存/更新/启用/删除/列举同一插件:opMu 应消除交错,-race 下无竞态/panic。
func TestConcurrentMutationsRace(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.CreatePlugin(map[string]any{"id": "c"}, "function onRequest(f){}"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 5 {
			case 0:
				_ = m.SavePluginSource("c", "function onRequest(f){ store.set('x',1); }")
			case 1:
				_ = m.UpdateManifest("c", map[string]any{"priority": float64(i + 1)})
			case 2:
				_ = m.EnablePlugin("c", i%2 == 0)
			case 3:
				_ = m.ClearPluginLogs("c")
			case 4:
				_ = m.ListPlugins()
			}
		}(i)
	}
	wg.Wait()
	if find(m.ListPlugins(), "c") == nil {
		t.Fatal("plugin lost after concurrent mutations")
	}
}

func TestUpdateManifest(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.CreatePlugin(map[string]any{"id": "upd", "priority": float64(100)}, "function onRequest(f){}"); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{
		"name":      "Renamed",
		"priority":  float64(5),
		"whitelist": []any{"*.example.com"},
	}
	if err := m.UpdateManifest("upd", patch); err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
	entry := find(m.ListPlugins(), "upd")
	if entry["name"] != "Renamed" || entry["priority"].(int) != 5 {
		t.Fatalf("manifest not updated: %+v", entry)
	}
}
