// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mintfog/sniffy/internal/pipeline"
)

// victimContent 必须能通过 JS 求值,否则读到目录外的文件会因求值失败被顺带挡下,验不到路径校验本身。
const victimContent = "// original\n"

// newEntryTestManager 把插件根目录放在临时目录深处,并在其外放一个受害文件。
// escape 是从插件目录指向该文件的相对路径(插件目录恒为插件根下的一层,与 id 无关)。
func newEntryTestManager(t *testing.T) (m *Manager, victim, escape string) {
	t.Helper()
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "config", "sniffy", "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	victim = filepath.Join(root, "victim.sh")
	if err := os.WriteFile(victim, []byte(victimContent), 0o644); err != nil {
		t.Fatal(err)
	}
	escape, err := filepath.Rel(filepath.Join(pluginRoot, "any"), victim)
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(pipeline.New(nil, nil), pluginRoot, nil, nil), victim, escape
}

func assertVictimIntact(t *testing.T, victim string) {
	t.Helper()
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("受害文件读取失败: %v", err)
	}
	if string(data) != victimContent {
		t.Fatalf("插件目录外的文件被改写: %q", data)
	}
}

// POST /api/plugins 的 manifest 整体透传给 CreatePlugin,entry 必须拒绝一切能逃出插件目录的写法。
func TestCreatePluginRejectsEscapingEntry(t *testing.T) {
	_, _, escape := newEntryTestManager(t)
	for _, entry := range []string{
		escape,
		"..",
		".",
		"sub/index.js",
		"/etc/cron.d/x",
		"../sibling.js",
	} {
		t.Run(entry, func(t *testing.T) {
			m, victim, _ := newEntryTestManager(t)
			meta := map[string]any{"id": "evil", "entry": entry}
			if _, err := m.CreatePlugin(meta, "// pwned\n"); err == nil {
				t.Fatal("非法入口本应被拒绝")
			}
			assertVictimIntact(t, victim)
			if _, err := os.Stat(filepath.Join(m.dir, "evil")); !os.IsNotExist(err) {
				t.Fatalf("被拒的创建不应留下目录: %v", err)
			}
			if find(m.ListPlugins(), "evil") != nil {
				t.Fatal("被拒的插件不应出现在列表")
			}
		})
	}
}

// 手工装入的插件包可以写任意 entry,加载时必须判为失败并回显原因,而不是照穿越路径读写。
func TestLoadAllRejectsEscapingEntryFromDisk(t *testing.T) {
	m, victim, escape := newEntryTestManager(t)
	dir := filepath.Join(m.dir, "evil")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(Manifest{ID: "evil", Entry: escape})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	entry := find(m.ListPlugins(), "evil")
	if entry == nil {
		t.Fatal("加载失败的插件应仍在列表里,带 error 字段")
	}
	if entry["error"] == nil {
		t.Fatalf("穿越入口的插件本应加载失败: %+v", entry)
	}
	if _, ok := m.GetPluginSource("evil"); ok {
		t.Fatal("不应能读到目录外文件的内容")
	}
	if err := m.SavePluginSource("evil", "// pwned\n"); err == nil {
		t.Fatal("不应能经保存源码写到目录外")
	}
	assertVictimIntact(t, victim)
}

// 入口是符号链接时,读写都不得跟随它跑到插件目录外。
func TestEntryRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 建符号链接需要额外权限")
	}
	m, victim, _ := newEntryTestManager(t)
	if _, err := m.CreatePlugin(map[string]any{"id": "sneaky"}, "function onRequest(f){}"); err != nil {
		t.Fatal(err)
	}
	entryFile := filepath.Join(m.dir, "sneaky", "index.js")
	if err := os.Remove(entryFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, entryFile); err != nil {
		t.Fatal(err)
	}

	if _, ok := m.GetPluginSource("sneaky"); ok {
		t.Fatal("不应经符号链接读出目录外文件")
	}
	if err := m.SavePluginSource("sneaky", "// pwned\n"); err == nil {
		t.Fatal("不应经符号链接写到目录外")
	}
	if err := m.UpdateManifest("sneaky", map[string]any{"name": "x"}); err == nil {
		t.Fatal("UpdateManifest 也不应经符号链接读源码")
	}
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if e := find(m.ListPlugins(), "sneaky"); e == nil || e["error"] == nil {
		t.Fatalf("符号链接入口的插件本应加载失败: %+v", e)
	}
	assertVictimIntact(t, victim)
}

// 合法入口(默认 index.js 与显式单层文件名)必须照常工作。
func TestCreatePluginAcceptsPlainEntry(t *testing.T) {
	for _, entry := range []string{"", "main.js", ".hidden.js"} {
		t.Run(entry, func(t *testing.T) {
			m, _, _ := newEntryTestManager(t)
			meta := map[string]any{"id": "good"}
			if entry != "" {
				meta["entry"] = entry
			}
			created, err := m.CreatePlugin(meta, "function onRequest(f){}")
			if err != nil {
				t.Fatalf("CreatePlugin: %v", err)
			}
			want := entry
			if want == "" {
				want = "index.js"
			}
			if created["entry"] != want {
				t.Fatalf("entry = %v, want %v", created["entry"], want)
			}
			if _, err := os.Stat(filepath.Join(m.dir, "good", want)); err != nil {
				t.Fatalf("入口脚本未写入: %v", err)
			}
			if err := m.SavePluginSource("good", "function onResponse(f){}"); err != nil {
				t.Fatalf("SavePluginSource: %v", err)
			}
		})
	}
}
