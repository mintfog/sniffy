// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// classify 按 errors.go 的约定拆解一个错误。
func classify(err error) (notFound, invalidInput bool) {
	var in interface{ InvalidInput() bool }
	return errors.Is(err, os.ErrNotExist), errors.As(err, &in) && in.InvalidInput()
}

// 只有「id 未知」能满足 os.ErrNotExist:入口脚本缺失等磁盘故障若也满足,
// 传输层会把它们读成「插件不存在」并回 404。
func TestErrorClassification(t *testing.T) {
	const src = "function onRequest(f){}"
	const badSrc = "function onRequest(f){ this is not valid js"

	t.Run("未知 id", func(t *testing.T) {
		m := newTestManager(t)
		if nf, inv := classify(m.EnablePlugin("ghost", true)); !nf || inv {
			t.Fatalf("notFound=%v invalidInput=%v", nf, inv)
		}
	})

	t.Run("入口脚本缺失", func(t *testing.T) {
		m := newTestManager(t)
		if _, err := m.CreatePlugin(map[string]any{"id": "e"}, src); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(m.dir, "e", "index.js")); err != nil {
			t.Fatal(err)
		}
		if nf, inv := classify(m.UpdateManifest("e", map[string]any{"name": "x"})); nf || inv {
			t.Fatalf("应归为磁盘故障: notFound=%v invalidInput=%v", nf, inv)
		}
	})

	t.Run("非法 id", func(t *testing.T) {
		m := newTestManager(t)
		_, err := m.CreatePlugin(map[string]any{"id": "BAD ID"}, src)
		if nf, inv := classify(err); nf || !inv {
			t.Fatalf("notFound=%v invalidInput=%v (err=%v)", nf, inv, err)
		}
	})

	t.Run("非法入口", func(t *testing.T) {
		m := newTestManager(t)
		_, err := m.CreatePlugin(map[string]any{"id": "x", "entry": "../evil.js"}, src)
		if nf, inv := classify(err); nf || !inv {
			t.Fatalf("notFound=%v invalidInput=%v (err=%v)", nf, inv, err)
		}
	})

	t.Run("id 冲突", func(t *testing.T) {
		m := newTestManager(t)
		if _, err := m.CreatePlugin(map[string]any{"id": "dup"}, src); err != nil {
			t.Fatal(err)
		}
		_, err := m.CreatePlugin(map[string]any{"id": "dup"}, src)
		if nf, inv := classify(err); nf || !inv {
			t.Fatalf("notFound=%v invalidInput=%v (err=%v)", nf, inv, err)
		}
	})

	t.Run("新建时源码编译不过", func(t *testing.T) {
		m := newTestManager(t)
		_, err := m.CreatePlugin(map[string]any{"id": "c"}, badSrc)
		if nf, inv := classify(err); nf || !inv {
			t.Fatalf("notFound=%v invalidInput=%v (err=%v)", nf, inv, err)
		}
	})

	t.Run("保存时源码编译不过", func(t *testing.T) {
		m := newTestManager(t)
		if _, err := m.CreatePlugin(map[string]any{"id": "s"}, src); err != nil {
			t.Fatal(err)
		}
		err := m.SavePluginSource("s", badSrc)
		if nf, inv := classify(err); nf || !inv {
			t.Fatalf("notFound=%v invalidInput=%v (err=%v)", nf, inv, err)
		}
	})

	// settings 在构建时注入 VM,顶层读它的脚本会因 patch 内容构建失败——错在调用方传的值。
	t.Run("改 settings 致构建失败", func(t *testing.T) {
		m := newTestManager(t)
		const picky = "if (settings.mode === 'boom') { throw new Error('bad settings'); }\nfunction onRequest(f){}"
		if _, err := m.CreatePlugin(map[string]any{"id": "u"}, picky); err != nil {
			t.Fatal(err)
		}
		err := m.UpdateManifest("u", map[string]any{"name": "u", "settings": map[string]any{"mode": "boom"}})
		if nf, inv := classify(err); nf || !inv {
			t.Fatalf("notFound=%v invalidInput=%v (err=%v)", nf, inv, err)
		}
	})

	t.Run("入口是符号链接", func(t *testing.T) {
		m := newTestManager(t)
		if _, err := m.CreatePlugin(map[string]any{"id": "n"}, src); err != nil {
			t.Fatal(err)
		}
		entry := filepath.Join(m.dir, "n", "index.js")
		target := filepath.Join(t.TempDir(), "outside.js")
		if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(entry); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, entry); err != nil {
			t.Skipf("当前环境无法创建符号链接: %v", err)
		}
		err := m.SavePluginSource("n", src)
		if nf, inv := classify(err); nf || !inv {
			t.Fatalf("notFound=%v invalidInput=%v (err=%v)", nf, inv, err)
		}
	})

	// 写盘目标消失属于磁盘故障:ENOENT 若原样透出,传输层会回 404 说「插件不存在」,
	// 而插件明明还在内存里跑着。
	t.Run("持久化时目录已消失", func(t *testing.T) {
		m := newTestManager(t)
		if _, err := m.CreatePlugin(map[string]any{"id": "g"}, src); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(m.dir, "g")); err != nil {
			t.Fatal(err)
		}
		err := m.EnablePlugin("g", false)
		if err == nil {
			t.Fatal("期望写盘失败")
		}
		if nf, inv := classify(err); nf || inv {
			t.Fatalf("应归为磁盘故障: notFound=%v invalidInput=%v (err=%v)", nf, inv, err)
		}
	})
}

// 落盘失败必须等于「什么都没发生」:内存里已生效而 plugin.json 是旧值时,UI 收到 500 后重拉
// 列表看到的却是「切换成功」,三方不一致,重启又跳回去。
func TestEnablePluginKeepsStateOnSaveFailure(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.CreatePlugin(map[string]any{"id": "k"}, "function onRequest(f){}"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(m.dir, "k")); err != nil {
		t.Fatal(err)
	}
	if err := m.EnablePlugin("k", true); err == nil {
		t.Fatal("期望写盘失败")
	}
	if entry := find(m.ListPlugins(), "k"); entry == nil || entry["enabled"] != false {
		t.Fatalf("落盘失败后内存态不应改变: %+v", entry)
	}
}
