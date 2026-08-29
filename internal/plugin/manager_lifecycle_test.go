// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/plugin/js"
)

// lcManager 使用真实管道装配 Manager，便于验证插件对 flow 的实际影响。
func lcManager(t *testing.T) (*Manager, *pipeline.Pipeline) {
	t.Helper()
	pipe := pipeline.New(nil, nil)
	m := NewManager(pipe, t.TempDir(), nil, nil)
	t.Cleanup(m.Close)
	return m, pipe
}

// lcAppendMarkSource 生成将标记追加到 X-Mark 的脚本，用于观察插件执行次数。
func lcAppendMarkSource(mark string) string {
	return "function onRequest(f){ header.set(f.headers, 'X-Mark', (header.get(f.headers,'X-Mark')||'') + '" + mark + ";'); }"
}

func lcFlow(url string) *flow.Flow {
	return &flow.Flow{
		ID:       "lc",
		Protocol: flow.ProtoHTTP,
		Request: &flow.Request{
			Method: "GET",
			URL:    url,
			Host:   "lc.test",
			Path:   "/x",
			Header: map[string][]string{"X-Keep": {"1"}},
		},
	}
}

func lcRun(t *testing.T, pipe *pipeline.Pipeline, url string) *flow.Flow {
	t.Helper()
	f := lcFlow(url)
	if d := pipe.OnRequest(context.Background(), f); d.Kind != flow.Continue {
		t.Fatalf("OnRequest 处置 = %v, 期望 Continue", d.Kind)
	}
	return f
}

func lcHeader(f *flow.Flow, name string) string {
	v := f.Request.Header[name]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// lcCreate 创建并启用一个插件。
func lcCreate(t *testing.T, m *Manager, id, source string, extra map[string]any) {
	t.Helper()
	meta := map[string]any{"id": id, "enabled": true}
	for k, v := range extra {
		meta[k] = v
	}
	if _, err := m.CreatePlugin(meta, source); err != nil {
		t.Fatalf("CreatePlugin(%s): %v", id, err)
	}
}

// lcWritePluginDir 在插件根目录下写入 manifest 与入口脚本。
func lcWritePluginDir(t *testing.T, root, name string, man Manifest, source string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(man)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 验证热重载通过 InitialStore 迁移 store，并按键处理不可序列化值。
func TestHotReloadKeepsStoreAcrossRebuild(t *testing.T) {
	// 测试脚本同时写入可序列化计数与 NaN，覆盖逐键迁移路径。
	const counting = `function onRequest(f){
  var n = (store.get('n') || 0) + 1;
  store.set('n', n);
  header.set(f.headers, 'X-Bad', String(store.get('bad')));
  store.set('bad', 0/0);
  header.set(f.headers, 'X-Count', String(n));
  header.set(f.headers, 'X-Tag', settings.tag || 'v1');
}`
	// 覆盖源码保存与 manifest 更新两条重载路径，并检查新实例配置。
	reloads := map[string]func(t *testing.T, m *Manager){
		"SavePluginSource": func(t *testing.T, m *Manager) {
			t.Helper()
			next := `function onRequest(f){
  var n = (store.get('n') || 0) + 1;
  store.set('n', n);
  header.set(f.headers, 'X-Bad', String(store.get('bad')));
  store.set('bad', 0/0);
  header.set(f.headers, 'X-Count', String(n));
  header.set(f.headers, 'X-Tag', 'v2');
}`
			if err := m.SavePluginSource("keeper", next); err != nil {
				t.Fatalf("SavePluginSource: %v", err)
			}
		},
		"UpdateManifest": func(t *testing.T, m *Manager) {
			t.Helper()
			patch := map[string]any{"settings": map[string]any{"tag": "v2"}}
			if err := m.UpdateManifest("keeper", patch); err != nil {
				t.Fatalf("UpdateManifest: %v", err)
			}
		},
	}
	for name, reload := range reloads {
		t.Run(name, func(t *testing.T) {
			m, pipe := lcManager(t)
			lcCreate(t, m, "keeper", counting, nil)

			for i := 1; i <= 3; i++ {
				f := lcRun(t, pipe, "http://lc.test/x")
				if got := lcHeader(f, "X-Count"); got != strconv.Itoa(i) {
					t.Fatalf("第 %d 次请求 X-Count = %q", i, got)
				}
				if got := lcHeader(f, "X-Tag"); got != "v1" {
					t.Fatalf("重载前 X-Tag = %q, 期望 v1", got)
				}
			}
			// 确认测试脚本已将 NaN 写入 store。
			if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Bad"); got != "NaN" {
				t.Fatalf("重载前 X-Bad = %q, 期望 NaN(不可序列化的值没进 store,本用例测不到降级)", got)
			}

			reload(t, m)

			f := lcRun(t, pipe, "http://lc.test/x")
			if got := lcHeader(f, "X-Tag"); got != "v2" {
				t.Fatalf("重载后 X-Tag = %q, 期望 v2(说明管道里还是旧实例)", got)
			}
			if got := lcHeader(f, "X-Count"); got != "5" {
				t.Fatalf("重载后 X-Count = %q, 期望 5(store 未迁移则会退回 1)", got)
			}
			if got := lcHeader(f, "X-Bad"); got != "null" {
				t.Fatalf("重载后 X-Bad = %q, 期望 null(不可序列化的键不该被迁移过去)", got)
			}
		})
	}
}

// 验证 Create、swap、Delete 后管道立即反映当前插件集合。
func TestPipelineTracksCreateSwapAndDelete(t *testing.T) {
	m, pipe := lcManager(t)

	if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Mark"); got != "" {
		t.Fatalf("空管道却改动了 flow: %q", got)
	}

	lcCreate(t, m, "swapper", lcAppendMarkSource("v1"), nil)
	f := lcRun(t, pipe, "http://lc.test/x")
	if got := lcHeader(f, "X-Mark"); got != "v1;" {
		t.Fatalf("Create 后 X-Mark = %q, 期望 v1;", got)
	}
	if !f.Modified {
		t.Fatal("插件改了头却没标记 Modified")
	}

	if err := m.SavePluginSource("swapper", lcAppendMarkSource("v2")); err != nil {
		t.Fatalf("SavePluginSource: %v", err)
	}
	if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Mark"); got != "v2;" {
		t.Fatalf("swap 后 X-Mark = %q, 期望 v2;(v1;v2; 说明新旧实例并存)", got)
	}

	if err := m.DeletePlugin("swapper"); err != nil {
		t.Fatalf("DeletePlugin: %v", err)
	}
	f = lcRun(t, pipe, "http://lc.test/x")
	if got := lcHeader(f, "X-Mark"); got != "" {
		t.Fatalf("删除后仍被改动: %q", got)
	}
	if f.Modified {
		t.Fatal("删除后 flow 仍被标记 Modified")
	}
}

// 验证禁用通过 Enabled() 门控生效，重新启用后立即恢复处理。
func TestPipelineSkipsDisabledPluginWithoutUnregistering(t *testing.T) {
	m, pipe := lcManager(t)
	lcCreate(t, m, "toggle", lcAppendMarkSource("on"), nil)

	if err := m.EnablePlugin("toggle", false); err != nil {
		t.Fatalf("EnablePlugin(false): %v", err)
	}
	if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Mark"); got != "" {
		t.Fatalf("已禁用插件仍改动了 flow: %q", got)
	}
	if find(m.ListPlugins(), "toggle") == nil {
		t.Fatal("禁用不应把插件从列表里摘掉")
	}

	if err := m.EnablePlugin("toggle", true); err != nil {
		t.Fatalf("EnablePlugin(true): %v", err)
	}
	if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Mark"); got != "on;" {
		t.Fatalf("重新启用后 X-Mark = %q, 期望 on;", got)
	}
}

// 验证 manifest priority 决定执行顺序，并在 UpdateManifest 后生效。
func TestPipelineOrderFollowsManifestPriority(t *testing.T) {
	m, pipe := lcManager(t)
	lcCreate(t, m, "first", lcAppendMarkSource("a"), map[string]any{"priority": float64(10)})
	lcCreate(t, m, "second", lcAppendMarkSource("b"), map[string]any{"priority": float64(20)})

	if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Mark"); got != "a;b;" {
		t.Fatalf("X-Mark = %q, 期望 a;b;(priority 小的先跑)", got)
	}

	// 更新 priority 后检查执行顺序反转。
	if err := m.UpdateManifest("first", map[string]any{"priority": float64(30)}); err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
	if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Mark"); got != "b;a;" {
		t.Fatalf("改优先级后 X-Mark = %q, 期望 b;a;", got)
	}
}

// 验证 manifest 中的白名单与黑名单经过构建后限制插件 URL 作用域。
func TestManifestListsGatePipelineByURL(t *testing.T) {
	m, pipe := lcManager(t)
	lcCreate(t, m, "gated", lcAppendMarkSource("hit"), nil)

	const allowed = "http://allowed.test/x"
	const denied = "http://denied.test/x"
	if got := lcHeader(lcRun(t, pipe, denied), "X-Mark"); got != "hit;" {
		t.Fatalf("无名单时应作用于全部 URL, X-Mark = %q", got)
	}

	if err := m.UpdateManifest("gated", map[string]any{"whitelist": []any{"http://allowed.test/*"}}); err != nil {
		t.Fatalf("UpdateManifest(whitelist): %v", err)
	}
	if got := lcHeader(lcRun(t, pipe, allowed), "X-Mark"); got != "hit;" {
		t.Fatalf("白名单内 URL 未被改动: %q", got)
	}
	if got := lcHeader(lcRun(t, pipe, denied), "X-Mark"); got != "" {
		t.Fatalf("白名单外 URL 被改动: %q", got)
	}

	patch := map[string]any{
		"whitelist": []any{"http://allowed.test/*"},
		"blacklist": []any{"*/x"},
	}
	if err := m.UpdateManifest("gated", patch); err != nil {
		t.Fatalf("UpdateManifest(blacklist): %v", err)
	}
	if got := lcHeader(lcRun(t, pipe, allowed), "X-Mark"); got != "" {
		t.Fatalf("黑名单未压过白名单: %q", got)
	}
}

// 验证 ListPlugins 返回 UI 所需字段及稳定排序。
func TestListPluginsFieldsOrderingAndLogs(t *testing.T) {
	m, pipe := lcManager(t)

	man := Manifest{
		ID: "b-ok", Name: "乙", Version: "2.1.0", Author: "作者", Description: "描述",
		Runtime: "js", Entry: "index.js", Enabled: true, Priority: 42,
		Whitelist: []string{"*"}, Blacklist: []string{"http://never/*"},
		Settings:       map[string]any{"tag": "t"},
		SettingsSchema: []SettingField{{Key: "tag", Type: "string"}},
	}
	lcWritePluginDir(t, m.dir, "b-ok", man, "function onRequest(f){ console.log('hit', f.url); }")
	lcWritePluginDir(t, m.dir, "a-broken", Manifest{ID: "a-broken", Entry: "index.js"}, "syntax (((")
	lcWritePluginDir(t, m.dir, "c-ok", Manifest{ID: "c-ok", Entry: "index.js", Enabled: true}, "function onRequest(f){}")

	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	lcRun(t, pipe, "http://lc.test/x")

	list := m.ListPlugins()
	ids := make([]string, 0, len(list))
	for _, e := range list {
		ids = append(ids, e["id"].(string))
	}
	want := []string{"a-broken", "b-ok", "c-ok"}
	if len(ids) != len(want) {
		t.Fatalf("条目数 = %v, 期望 %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("加载成功与失败的条目未按 id 升序混排: %v", ids)
		}
	}

	entry := find(list, "b-ok")
	for _, key := range []string{
		"id", "name", "version", "description", "author", "runtime",
		"enabled", "priority", "whitelist", "blacklist", "settings", "settingsSchema", "logs",
	} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("缺字段 %q: %+v", key, entry)
		}
	}
	if entry["name"] != "乙" || entry["version"] != "2.1.0" || entry["author"] != "作者" ||
		entry["description"] != "描述" || entry["runtime"] != "js" || entry["enabled"] != true {
		t.Fatalf("元信息未如实回报: %+v", entry)
	}
	if p, _ := entry["priority"].(int); p != 42 {
		t.Fatalf("priority = %v, 期望 42", entry["priority"])
	}
	if wl, _ := entry["whitelist"].([]string); len(wl) != 1 || wl[0] != "*" {
		t.Fatalf("whitelist = %+v", entry["whitelist"])
	}
	if bl, _ := entry["blacklist"].([]string); len(bl) != 1 {
		t.Fatalf("blacklist = %+v", entry["blacklist"])
	}
	if s, _ := entry["settings"].(map[string]any); s["tag"] != "t" {
		t.Fatalf("settings = %+v", entry["settings"])
	}
	if sc, _ := entry["settingsSchema"].([]SettingField); len(sc) != 1 || sc[0].Key != "tag" {
		t.Fatalf("settingsSchema = %+v", entry["settingsSchema"])
	}

	logs, ok := entry["logs"].([]js.LogEntry)
	if !ok {
		t.Fatalf("logs 类型异常: %T", entry["logs"])
	}
	found := false
	for _, e := range logs {
		if e.Level == "log" && containsStr(e.Msg, "http://lc.test/x") {
			found = true
		}
	}
	if !found {
		t.Fatalf("logs 未带上该插件跑请求时的 console 输出: %+v", logs)
	}
	// 验证日志按插件实例隔离。
	if other, _ := find(list, "c-ok")["logs"].([]js.LogEntry); len(other) != 0 {
		t.Fatalf("日志串到了别的插件: %+v", other)
	}

	broken := find(list, "a-broken")
	if broken["error"] == nil || broken["enabled"] != false {
		t.Fatalf("失败条目字段异常: %+v", broken)
	}
}

// 验证重复 LoadAll 先关闭旧实例，再重建管道与插件。
func TestLoadAllTwiceClosesOldInstances(t *testing.T) {
	m, pipe := lcManager(t)
	const counting = `function onRequest(f){
  var n = (store.get('n') || 0) + 1;
  store.set('n', n);
  header.set(f.headers, 'X-Count', String(n));
  header.set(f.headers, 'X-Mark', (header.get(f.headers,'X-Mark')||'') + 'r;');
}`
	lcWritePluginDir(t, m.dir, "reloader", Manifest{ID: "reloader", Entry: "index.js", Enabled: true}, counting)

	if err := m.LoadAll(); err != nil {
		t.Fatalf("首次 LoadAll: %v", err)
	}
	for i := 1; i <= 2; i++ {
		lcRun(t, pipe, "http://lc.test/x")
	}

	if err := m.LoadAll(); err != nil {
		t.Fatalf("再次 LoadAll: %v", err)
	}
	if got := len(m.ListPlugins()); got != 1 {
		t.Fatalf("重复 LoadAll 后条目数 = %d, 期望 1", got)
	}
	f := lcRun(t, pipe, "http://lc.test/x")
	if got := lcHeader(f, "X-Mark"); got != "r;" {
		t.Fatalf("X-Mark = %q, 期望 r;(重复标记说明同一插件在管道里有两份)", got)
	}
	// 新实例从 state.json 恢复计数，覆盖关闭时的最终落盘。
	if got := lcHeader(f, "X-Count"); got != "3" {
		t.Fatalf("X-Count = %q, 期望 3(旧实例未 Close 落盘则退回 1)", got)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "reloader", "state.json")); err != nil {
		t.Fatalf("旧实例未把 store 落盘: %v", err)
	}
}

// 验证 plugins 与 failed 使用各自索引，删除同一 id 后两侧状态均收敛。
func TestDeleteConvergesOnIDSharedWithFailedDir(t *testing.T) {
	m, _ := lcManager(t)
	// 目录名与 manifest id 不同，创建一个占用 id "shared" 的已加载插件。
	lcWritePluginDir(t, m.dir, "carrier", Manifest{ID: "shared", Entry: "index.js", Enabled: true}, "function onRequest(f){}")
	// 目录名为 "shared" 的插件加载失败后进入 failed 表。
	lcWritePluginDir(t, m.dir, "shared", Manifest{ID: "shared", Entry: "index.js"}, "syntax (((")

	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	count := 0
	for _, e := range m.ListPlugins() {
		if e["id"] == "shared" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("未构造出 id 冲突,已加载+失败条目数 = %d", count)
	}

	if err := m.DeletePlugin("shared"); err != nil {
		t.Fatalf("首次 DeletePlugin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "carrier")); !os.IsNotExist(err) {
		t.Fatalf("已加载实例的目录未删除: %v", err)
	}
	rest := find(m.ListPlugins(), "shared")
	if rest == nil || rest["error"] == nil {
		t.Fatalf("失败条目应仍可见,以便用户接着删除: %+v", rest)
	}

	if err := m.DeletePlugin("shared"); err != nil {
		t.Fatalf("再次 DeletePlugin: %v", err)
	}
	if find(m.ListPlugins(), "shared") != nil {
		t.Fatal("两次删除后 id 仍在列表里")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "shared")); !os.IsNotExist(err) {
		t.Fatalf("失败条目的目录未删除: %v", err)
	}
	// 删除后目录与内存状态均清理，同一 id 可再次创建。
	if _, err := m.CreatePlugin(map[string]any{"id": "shared"}, "function onRequest(f){}"); err != nil {
		t.Fatalf("删除后重建同 id 失败: %v", err)
	}
}

// 验证 failed 条目在 RemoveAll 成功后从列表移除。
func TestDeleteFailedEntryStaysListedWhenRemoveFails(t *testing.T) {
	m, _ := lcManager(t)
	lcWritePluginDir(t, m.dir, "broken", Manifest{ID: "broken", Entry: "index.js"}, "syntax (((")
	if err := m.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if e := find(m.ListPlugins(), "broken"); e == nil || e["error"] == nil {
		t.Fatalf("前置条件不成立,broken 未进失败表: %+v", e)
	}

	// 父目录只读时目录项无法 unlink，RemoveAll 返回错误。
	if err := os.Chmod(m.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	restore := func() { _ = os.Chmod(m.dir, 0o755) }
	t.Cleanup(restore)
	// 按实际文件系统权限探测可写性，目录可写时执行该场景。
	probe := filepath.Join(m.dir, ".probe")
	if f, err := os.Create(probe); err == nil {
		f.Close()
		_ = os.Remove(probe)
		restore()
		t.Skip("当前环境不受目录写权限约束,无法构造 RemoveAll 失败")
	}

	if err := m.DeletePlugin("broken"); err == nil {
		t.Fatal("父目录只读时 DeletePlugin 应报错")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "broken")); err != nil {
		t.Fatalf("删盘失败后目录本应仍在盘上: %v", err)
	}
	if e := find(m.ListPlugins(), "broken"); e == nil || e["error"] == nil {
		t.Fatalf("删盘失败后失败条目从列表消失,用户再也删不掉它: %+v", e)
	}

	restore()
	if err := m.DeletePlugin("broken"); err != nil {
		t.Fatalf("权限恢复后重删应成功: %v", err)
	}
	if find(m.ListPlugins(), "broken") != nil {
		t.Fatal("删除成功后条目仍在列表里")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "broken")); !os.IsNotExist(err) {
		t.Fatalf("目录未被删除: %v", err)
	}
}

// 验证未配置插件目录时 LoadAll 直接返回并保留现有管道钩子。
func TestLoadAllWithoutDirKeepsPipelineIntact(t *testing.T) {
	pipe := pipeline.New(nil, nil)
	withDir := NewManager(pipe, t.TempDir(), nil, nil)
	t.Cleanup(withDir.Close)
	lcCreate(t, withDir, "resident", lcAppendMarkSource("keep"), nil)

	noDir := NewManager(pipe, "", nil, nil)
	t.Cleanup(noDir.Close)
	if err := noDir.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got := len(noDir.ListPlugins()); got != 0 {
		t.Fatalf("未配置目录却列出了插件: %d", got)
	}
	if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Mark"); got != "keep;" {
		t.Fatalf("早退的 LoadAll 清空了管道, X-Mark = %q", got)
	}
}

// 验证热重载与请求处理并发时保持安全，标记来自完整的旧版或新版源码。
func TestHotReloadConcurrentWithTraffic(t *testing.T) {
	m, pipe := lcManager(t)
	lcCreate(t, m, "hot", lcAppendMarkSource("v1"), nil)

	var (
		mu    sync.Mutex
		marks []string
		wg    sync.WaitGroup
	)
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			f := lcFlow("http://lc.test/x")
			pipe.OnRequest(context.Background(), f)
			mu.Lock()
			marks = append(marks, lcHeader(f, "X-Mark"))
			mu.Unlock()
		}
	}()

	var saveErr error
	for i := 0; i < 10; i++ {
		mark := "v1"
		if i%2 == 1 {
			mark = "v2"
		}
		if err := m.SavePluginSource("hot", lcAppendMarkSource(mark)); err != nil {
			saveErr = err
			break
		}
	}
	close(stop)
	wg.Wait()
	if saveErr != nil {
		t.Fatalf("并发热重载中保存失败: %v", saveErr)
	}

	marked := 0
	for _, got := range marks {
		switch got {
		case "v1;", "v2;":
			marked++
		case "":
			// 管道重建期间的请求按 Continue 语义处理。
		default:
			t.Fatalf("并发重载期间出现损坏的标记: %q", got)
		}
	}
	if len(marks) == 0 {
		t.Fatal("并发期间一条 flow 都没跑完")
	}
	if marked == 0 {
		t.Fatalf("并发期间没有任何 flow 被插件处理过(共 %d 条)", len(marks))
	}
	// 重载完成后管道使用最后一次保存的源码。
	if got := lcHeader(lcRun(t, pipe, "http://lc.test/x"), "X-Mark"); got != "v2;" {
		t.Fatalf("重载结束后 X-Mark = %q, 期望 v2;", got)
	}
}

// 验证未知 id 的错误同时支持 errors.Is(os.ErrNotExist) 与包含 id 的错误文案。
func TestUnknownIDErrorIsClassifiableAndReadable(t *testing.T) {
	m, _ := lcManager(t)
	err := m.DeletePlugin("ghost")
	if err == nil {
		t.Fatal("删除未知 id 应报错")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("未被归为 404: %v", err)
	}
	if !containsStr(err.Error(), "ghost") {
		t.Fatalf("文案未带上插件 id: %q", err.Error())
	}
	if err.Error() == os.ErrNotExist.Error() {
		t.Fatalf("文案退回了裸 os.ErrNotExist: %q", err.Error())
	}
}
