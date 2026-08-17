// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/pipeline"
)

// 插件目录里的散落文件只应被忽略,既不算插件也不能被改动。
func TestLoadAllSkipsNonDirEntries(t *testing.T) {
	m := newTestManager(t)
	stray := filepath.Join(m.dir, "README.md")
	if err := os.WriteFile(stray, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(m.dir, "good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "plugin.json"), []byte(`{"id":"good"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "index.js"), []byte("function onRequest(f){}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	list := m.ListPlugins()
	if find(list, "good") == nil {
		t.Fatalf("正常插件未加载: %+v", list)
	}
	if len(list) != 1 {
		t.Fatalf("散落文件被当成了插件条目: %+v", list)
	}
	data, err := os.ReadFile(stray)
	if err != nil || string(data) != "keep me" {
		t.Fatalf("散落文件被改动: %q err=%v", data, err)
	}
}

// loadOne 的两条读盘失败分支:缺 plugin.json、缺入口脚本;都应记为加载失败并保留原因。
func TestLoadOneMissingManifestAndEntry(t *testing.T) {
	m := newTestManager(t)
	if err := os.MkdirAll(filepath.Join(m.dir, "no-manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	noEntry := filepath.Join(m.dir, "no-entry")
	if err := os.MkdirAll(noEntry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noEntry, "plugin.json"), []byte(`{"id":"no-entry"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	list := m.ListPlugins()
	e1 := find(list, "no-manifest")
	if e1 == nil {
		t.Fatalf("缺 manifest 的目录未出现在列表: %+v", list)
	}
	if msg, _ := e1["error"].(string); !containsStr(msg, "plugin.json") {
		t.Fatalf("错误信息未指明缺失的是 plugin.json: %q", msg)
	}
	e2 := find(list, "no-entry")
	if e2 == nil {
		t.Fatalf("缺入口脚本的目录未出现在列表: %+v", list)
	}
	if msg, _ := e2["error"].(string); !containsStr(msg, "index.js") {
		t.Fatalf("错误信息未指明缺失的是入口脚本: %q", msg)
	}
}

// 源码写盘失败时 SavePluginSource 必须报错,且运行中的旧实例不被新实例顶掉。
func TestSavePluginSourceWriteFailure(t *testing.T) {
	m := newTestManager(t)
	orig := "function onRequest(f){}"
	if _, err := m.CreatePlugin(map[string]any{"id": "w", "name": "保持"}, orig); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(m.dir, "w", "index.js")
	// 把入口路径换成目录:WriteFile 必然失败,且与运行权限无关。
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := m.SavePluginSource("w", "function onResponse(f){}"); err == nil {
		t.Fatal("expected write error")
	}
	if e := find(m.ListPlugins(), "w"); e == nil || e["name"] != "保持" {
		t.Fatalf("插件在保存失败后丢失或被改写: %+v", e)
	}
	// 障碍移除后仍能正常保存,说明旧实例没有被失败路径关掉。
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := m.SavePluginSource("w", "function onResponse(f){}"); err != nil {
		t.Fatalf("恢复后保存失败: %v", err)
	}
	if src, ok := m.GetPluginSource("w"); !ok || src != "function onResponse(f){}" {
		t.Fatalf("源码未写入: ok=%v src=%q", ok, src)
	}
}

// CreatePlugin 的三条落盘失败分支都必须报错,且不留下半截插件目录。
func TestCreatePluginDiskFailures(t *testing.T) {
	t.Run("插件根目录是文件时建目录失败", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "root")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		m := NewManager(pipeline.New(nil, nil), blocker, nil, nil)
		if _, err := m.CreatePlugin(map[string]any{"id": "p"}, "function onRequest(f){}"); err == nil {
			t.Fatal("expected mkdir error")
		}
		if got := len(m.ListPlugins()); got != 0 {
			t.Fatalf("建目录失败却登记了插件: %d", got)
		}
	})
	t.Run("settings 不可序列化时回滚目录", func(t *testing.T) {
		m := newTestManager(t)
		meta := map[string]any{"id": "bad-settings", "settings": map[string]any{"ch": make(chan int)}}
		if _, err := m.CreatePlugin(meta, "function onRequest(f){}"); err == nil {
			t.Fatal("expected manifest marshal error")
		}
		if _, err := os.Stat(filepath.Join(m.dir, "bad-settings")); !os.IsNotExist(err) {
			t.Fatalf("目录未回滚: %v", err)
		}
		if find(m.ListPlugins(), "bad-settings") != nil {
			t.Fatal("失败的插件不应出现在列表")
		}
	})
	t.Run("入口路径不可写时回滚目录", func(t *testing.T) {
		m := newTestManager(t)
		// 超长文件名能过单层文件名校验,但 OS 拒绝创建,借此走到写脚本失败的分支(manifest 已先写成功)。
		meta := map[string]any{"id": "bad-entry", "entry": strings.Repeat("n", 300) + ".js"}
		if _, err := m.CreatePlugin(meta, "function onRequest(f){}"); err == nil {
			t.Fatal("expected entry write error")
		}
		if _, err := os.Stat(filepath.Join(m.dir, "bad-entry")); !os.IsNotExist(err) {
			t.Fatalf("目录未回滚: %v", err)
		}
		if find(m.ListPlugins(), "bad-entry") != nil {
			t.Fatal("失败的插件不应出现在列表")
		}
	})
}

// manifest 落盘失败时 UpdateManifest 必须报错,内存与磁盘上的 manifest 都保持原样。
func TestUpdateManifestSaveFailure(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.CreatePlugin(map[string]any{"id": "u", "name": "orig", "priority": float64(20)}, "function onRequest(f){}"); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(m.dir, "u", "plugin.json")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	patch := map[string]any{
		"name":     "renamed",
		"priority": float64(30),
		"settings": map[string]any{"ch": make(chan int)},
	}
	if err := m.UpdateManifest("u", patch); err == nil {
		t.Fatal("expected manifest marshal error")
	}
	entry := find(m.ListPlugins(), "u")
	if entry == nil || entry["name"] != "orig" {
		t.Fatalf("内存 manifest 被失败的更新改写: %+v", entry)
	}
	if p, _ := entry["priority"].(int); p != 20 {
		t.Fatalf("priority 被失败的更新改写: %v", entry["priority"])
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("磁盘 manifest 被改动:\n before %s\n after %s", before, after)
	}
}

// 目录删不掉时 DeletePlugin 应把错误透出;此时实例已从内存摘除,目录仍留在磁盘上。
func TestDeletePluginRemoveFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("依赖 POSIX 目录写权限语义,且 root 会绕过检查")
	}
	m := newTestManager(t)
	if _, err := m.CreatePlugin(map[string]any{"id": "locked"}, "function onRequest(f){}"); err != nil {
		t.Fatal(err)
	}
	// 父目录只读 → 子目录条目无法被 unlink。
	if err := os.Chmod(m.dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(m.dir, 0o755) })

	if err := m.DeletePlugin("locked"); err == nil {
		t.Fatal("expected remove error")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "locked")); err != nil {
		t.Fatalf("目录本应仍在磁盘上: %v", err)
	}
	if find(m.ListPlugins(), "locked") != nil {
		t.Fatal("删除失败后实例仍应已从内存摘除")
	}
}

// logf 只认 error/info 两个级别,其余一律落到 Debug;logger 为 nil 时静默。
func TestLogfLevelRouting(t *testing.T) {
	spy := &spyLogger{}
	m := NewManager(pipeline.New(nil, nil), t.TempDir(), spy, nil)
	m.logf("trace", "值=%v", 42)

	if !spy.hasArgContaining("debug", "42") {
		t.Fatalf("未知级别未落到 Debug: %+v", spy.calls)
	}
	if spy.hasArgContaining("info", "42") || spy.hasArgContaining("error", "42") {
		t.Fatalf("未知级别串到了 info/error: %+v", spy.calls)
	}
	// logger 为 nil 时不得 panic。
	NewManager(pipeline.New(nil, nil), "", nil, nil).logf("info", "x")
}
