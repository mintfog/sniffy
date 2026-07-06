// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// spyLogger 记录每次日志调用的级别与格式串,供断言日志契约。
type spyLogger struct {
	mu    sync.Mutex
	calls []spyCall
}

type spyCall struct {
	level  string
	format string
	args   []any
}

func (s *spyLogger) Info(msg string, args ...any)  { s.record("info", msg, args) }
func (s *spyLogger) Error(msg string, args ...any) { s.record("error", msg, args) }
func (s *spyLogger) Debug(msg string, args ...any) { s.record("debug", msg, args) }

func (s *spyLogger) record(level, format string, args []any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, spyCall{level: level, format: format, args: args})
}

// hasArgContaining 扫描指定 level 日志的每个结构化 arg,判断是否有值(%v 展开后)包含子串。
// 只看 args,不看格式串 —— 这样文案换措辞不会误伤断言。
func (s *spyLogger) hasArgContaining(level, substring string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if c.level != level {
			continue
		}
		for _, a := range c.args {
			if containsStr(fmt.Sprintf("%v", a), substring) {
				return true
			}
		}
	}
	return false
}

// hasArgOfErrorType 判断指定 level 是否至少有一条日志携带 error 类型的 arg。
func (s *spyLogger) hasArgOfErrorType(level string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if c.level != level {
			continue
		}
		for _, a := range c.args {
			if _, ok := a.(error); ok {
				return true
			}
		}
	}
	return false
}

func containsStr(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Close 应释放已加载实例、清空 plugins,且可被多次调用而不 panic。
func TestCloseIsIdempotentAndClearsPlugins(t *testing.T) {
	m := newTestManager(t)
	src := "function onRequest(f){}"
	if _, err := m.CreatePlugin(map[string]any{"id": "a"}, src); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePlugin(map[string]any{"id": "b"}, src); err != nil {
		t.Fatal(err)
	}
	if got := len(m.ListPlugins()); got != 2 {
		t.Fatalf("pre-close count = %d, want 2", got)
	}
	m.Close()
	if got := len(m.ListPlugins()); got != 0 {
		t.Fatalf("post-close list not empty: %d", got)
	}
	m.mu.RLock()
	inner := len(m.plugins)
	m.mu.RUnlock()
	if inner != 0 {
		t.Fatalf("plugins map not cleared: %d", inner)
	}
	// 再次 Close 不应 panic。
	m.Close()
}

// LoadAll 成功/失败均应通过 logger 暴露 info/error 事件,便于运维观测。
func TestLoadAllLogsInfoAndErrorViaSpyLogger(t *testing.T) {
	tmp := t.TempDir()
	spy := &spyLogger{}
	m := NewManager(pipeline.New(nil, nil), tmp, spy, nil)

	goodDir := filepath.Join(tmp, "good")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(goodDir, "plugin.json"), []byte(`{"id":"good","entry":"index.js"}`), 0o644)
	os.WriteFile(filepath.Join(goodDir, "index.js"), []byte("function onRequest(f){}"), 0o644)

	badDir := filepath.Join(tmp, "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(badDir, "plugin.json"), []byte(`{"id":"bad","entry":"index.js"}`), 0o644)
	os.WriteFile(filepath.Join(badDir, "index.js"), []byte("syntax ((("), 0o644)

	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	// 只断结构化 args 内容,不断格式串 —— 文案换措辞不应弄坏这条测试。
	if !spy.hasArgContaining("info", "good") {
		t.Fatalf("info log missing success entry for good plugin: %+v", spy.calls)
	}
	if !spy.hasArgContaining("error", "bad") {
		t.Fatalf("error log missing failure entry for bad plugin: %+v", spy.calls)
	}
	if !spy.hasArgOfErrorType("error") {
		t.Fatalf("error log missing error-typed arg: %+v", spy.calls)
	}
	// failed map 的可观测性由 TestFailedLoadSurfaced 通过 ListPlugins 公开 API 覆盖,这里不再重复探私有 map。
}

// LoadAll 在空 dir 与不存在 dir 两条边界:静默返回 nil / 报 PathError 而非 panic。
func TestLoadAllEmptyDirNoOpAndReadDirError(t *testing.T) {
	t.Run("empty dir string is silent no-op", func(t *testing.T) {
		m := NewManager(pipeline.New(nil, nil), "", nil, nil)
		if err := m.LoadAll(); err != nil {
			t.Fatalf("LoadAll: %v", err)
		}
		if got := len(m.ListPlugins()); got != 0 {
			t.Fatalf("empty dir: expected 0 plugins, got %d", got)
		}
	})
	t.Run("unreadable dir surfaces error", func(t *testing.T) {
		// 让父路径是文件而非目录:seedExamples 与 LoadAll 的 ReadDir 都会失败。
		tmp := t.TempDir()
		blocker := filepath.Join(tmp, "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		bad := filepath.Join(blocker, "sub")
		m := NewManager(pipeline.New(nil, nil), bad, nil, nil)
		err := m.LoadAll()
		if err == nil {
			t.Fatal("expected error for unreadable dir")
		}
		if got := len(m.ListPlugins()); got != 0 {
			t.Fatalf("expected 0 plugins on error, got %d", got)
		}
	})
}

// 空目录下 LoadAll 应写入示例插件并将其加载起来。
func TestSeedExamplesOnEmptyDir(t *testing.T) {
	m := newTestManager(t)
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	// 磁盘上应写入示例目录与两个文件。
	for _, name := range []string{"plugin.json", "index.js"} {
		p := filepath.Join(m.dir, "example-add-header", name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("seed file missing: %s: %v", p, err)
		}
	}
	entry := find(m.ListPlugins(), "example-add-header")
	if entry == nil {
		t.Fatal("example plugin not listed after seed")
	}
	if enabled, _ := entry["enabled"].(bool); enabled {
		t.Fatal("example plugin should default to disabled")
	}
	// 再次 LoadAll 目录已非空,不应重复 seed(依赖已有条目分支)。
	if err := m.LoadAll(); err != nil {
		t.Fatalf("second LoadAll: %v", err)
	}
	if entry := find(m.ListPlugins(), "example-add-header"); entry == nil {
		t.Fatal("example plugin lost after second LoadAll")
	}
}

// CreatePlugin 遇到编译错误时应回滚已建目录,同 id 后续再建仍可成功。
func TestCreatePluginRollbackOnBadSource(t *testing.T) {
	m := newTestManager(t)
	_, err := m.CreatePlugin(map[string]any{"id": "bad"}, "function onRequest(f){ this is not valid js")
	if err == nil {
		t.Fatal("expected compile error")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "bad")); !os.IsNotExist(err) {
		t.Fatalf("dir not rolled back: %v", err)
	}
	if find(m.ListPlugins(), "bad") != nil {
		t.Fatal("failed plugin should not be listed")
	}
	// 目录已被清干净,后续同 id 用合法源码仍应可创建。
	if _, err := m.CreatePlugin(map[string]any{"id": "bad"}, "function onRequest(f){}"); err != nil {
		t.Fatalf("retry create after rollback: %v", err)
	}
}

// 未知 id 与 index.js 缺失两种情况下,GetPluginSource 都应返回 ("", false)。
func TestGetPluginSourceErrors(t *testing.T) {
	m := newTestManager(t)
	if src, ok := m.GetPluginSource("unknown"); ok || src != "" {
		t.Fatalf("unknown id: got (%q,%v)", src, ok)
	}
	if _, err := m.CreatePlugin(map[string]any{"id": "gone"}, "function onRequest(f){}"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(m.dir, "gone", "index.js")); err != nil {
		t.Fatal(err)
	}
	if src, ok := m.GetPluginSource("gone"); ok || src != "" {
		t.Fatalf("missing entry: got (%q,%v)", src, ok)
	}
}

// SavePluginSource 不应替不存在的 id 自动新建目录。
func TestSavePluginSourceMissing(t *testing.T) {
	m := newTestManager(t)
	err := m.SavePluginSource("ghost", "function onRequest(f){}")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(m.dir, "ghost")); !os.IsNotExist(statErr) {
		t.Fatalf("ghost dir should not be created: %v", statErr)
	}
}

// EnablePlugin:未知 id 报 NotExist;已知 id 的状态应写入 plugin.json 并可被 ListPlugins 反映。
func TestEnablePluginMissingAndPersistence(t *testing.T) {
	m := newTestManager(t)
	if err := m.EnablePlugin("ghost", true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	if _, err := m.CreatePlugin(map[string]any{"id": "p", "enabled": false}, "function onRequest(f){}"); err != nil {
		t.Fatal(err)
	}
	if err := m.EnablePlugin("p", true); err != nil {
		t.Fatalf("EnablePlugin: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "p", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var disk Manifest
	if err := json.Unmarshal(data, &disk); err != nil {
		t.Fatal(err)
	}
	if !disk.Enabled {
		t.Fatal("Enabled=true 未落盘")
	}
	if entry := find(m.ListPlugins(), "p"); entry == nil || entry["enabled"] != true {
		t.Fatalf("in-memory enabled not reflected: %+v", entry)
	}
}

// UpdateManifest 的三条失败路径:未知 id、入口脚本缺失、脚本被替换为非法源码。
func TestUpdateManifestErrorPaths(t *testing.T) {
	t.Run("unknown id", func(t *testing.T) {
		m := newTestManager(t)
		if err := m.UpdateManifest("ghost", map[string]any{}); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("want ErrNotExist, got %v", err)
		}
	})
	t.Run("entry file missing", func(t *testing.T) {
		m := newTestManager(t)
		if _, err := m.CreatePlugin(map[string]any{"id": "e", "name": "orig"}, "function onRequest(f){}"); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(m.dir, "e", "index.js")); err != nil {
			t.Fatal(err)
		}
		err := m.UpdateManifest("e", map[string]any{"name": "x"})
		if err == nil {
			t.Fatal("expected read error")
		}
		if entry := find(m.ListPlugins(), "e"); entry == nil || entry["name"] != "orig" {
			t.Fatalf("manifest changed despite failure: %+v", entry)
		}
	})
	t.Run("entry file has syntax error", func(t *testing.T) {
		m := newTestManager(t)
		if _, err := m.CreatePlugin(map[string]any{"id": "s", "name": "orig"}, "function onRequest(f){}"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.dir, "s", "index.js"), []byte("syntax ((("), 0o644); err != nil {
			t.Fatal(err)
		}
		err := m.UpdateManifest("s", map[string]any{"name": "y"})
		if err == nil {
			t.Fatal("expected compile error")
		}
		if entry := find(m.ListPlugins(), "s"); entry == nil || entry["name"] != "orig" {
			t.Fatalf("manifest patched despite compile failure: %+v", entry)
		}
	})
}

// CreatePlugin 应容纳 settingsSchema 合法数组与 int64 优先级;非数组 schema 应被静默丢弃。
// 顺带覆盖 asStringSlice 的空字符串过滤分支。
func TestCreatePluginSettingsSchemaAndInt64Priority(t *testing.T) {
	m := newTestManager(t)
	schema := []any{map[string]any{
		"key":     "foo",
		"type":    "string",
		"default": "x",
	}}
	src := "function onRequest(f){}"
	meta := map[string]any{
		"id":             "s1",
		"settingsSchema": schema,
		"priority":       int64(7),
		"whitelist":      []any{"*.example.com", ""},
	}
	if _, err := m.CreatePlugin(meta, src); err != nil {
		t.Fatal(err)
	}
	entry := find(m.ListPlugins(), "s1")
	if entry == nil {
		t.Fatal("s1 not listed")
	}
	if p, _ := entry["priority"].(int); p != 7 {
		t.Fatalf("priority(int64) not respected: got %v", entry["priority"])
	}
	fields, ok := entry["settingsSchema"].([]SettingField)
	if !ok || len(fields) != 1 || fields[0].Key != "foo" {
		t.Fatalf("settingsSchema not parsed: %+v", entry["settingsSchema"])
	}
	wl, _ := entry["whitelist"].([]string)
	if len(wl) != 1 || wl[0] != "*.example.com" {
		t.Fatalf("whitelist empty-string not filtered: %+v", wl)
	}

	// 非数组 settingsSchema 走 Unmarshal 失败分支,应返回 nil 而非报错。
	bad := map[string]any{
		"id":             "s2",
		"settingsSchema": "not-an-array",
	}
	if _, err := m.CreatePlugin(bad, src); err != nil {
		t.Fatalf("CreatePlugin with bad schema should not error: %v", err)
	}
	entry2 := find(m.ListPlugins(), "s2")
	if entry2 == nil {
		t.Fatal("s2 not listed")
	}
	if fields, _ := entry2["settingsSchema"].([]SettingField); fields != nil {
		t.Fatalf("bad schema should be dropped to nil, got %+v", fields)
	}
}

// DeletePlugin 需处理未知 id 与加载失败条目两条分支。
func TestDeletePluginErrorAndFailedEntry(t *testing.T) {
	t.Run("unknown id", func(t *testing.T) {
		m := newTestManager(t)
		if err := m.DeletePlugin("ghost"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("want ErrNotExist, got %v", err)
		}
	})
	t.Run("failed plugin", func(t *testing.T) {
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
		if find(m.ListPlugins(), "broken") == nil {
			t.Fatal("broken failed entry not surfaced")
		}
		if err := m.DeletePlugin("broken"); err != nil {
			t.Fatalf("DeletePlugin: %v", err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("broken dir not removed: %v", err)
		}
		m.mu.RLock()
		_, still := m.failed["broken"]
		m.mu.RUnlock()
		if still {
			t.Fatal("broken still in failed map")
		}
		if find(m.ListPlugins(), "broken") != nil {
			t.Fatal("broken still listed after delete")
		}
	})
}

// buildPlugin 的 OnLog 分支:注入 Emitter 后,console.log 应触发 plugin_log 事件。
func TestBuildPluginEmitterReceivesLogs(t *testing.T) {
	events := make(chan map[string]any, 16)
	emit := func(evt string, payload any) {
		if evt != "plugin_log" {
			return
		}
		p, _ := payload.(map[string]any)
		select {
		case events <- p:
		default:
		}
	}
	tmp := t.TempDir()
	m := NewManager(pipeline.New(nil, nil), tmp, nil, emit)
	src := "console.log('boot');\nfunction onRequest(f){}"
	if _, err := m.CreatePlugin(map[string]any{"id": "log"}, src); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case p := <-events:
			if p["id"] != "log" {
				continue
			}
			if p["level"] != "log" {
				t.Fatalf("unexpected level: %+v", p)
			}
			if p["msg"] != "boot" {
				t.Fatalf("unexpected msg: %+v", p)
			}
			if ts, _ := p["time"].(int64); ts <= 0 {
				t.Fatalf("time not set: %+v", p)
			}
			return
		case <-deadline:
			t.Fatal("emitter did not receive plugin_log within 1s")
		}
	}
}

// ClearPluginLogs 遇到未知 id 应返回 NotExist,保持与其他 id 查询错误的契约一致。
func TestClearPluginLogsMissing(t *testing.T) {
	m := newTestManager(t)
	if err := m.ClearPluginLogs("ghost"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
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
