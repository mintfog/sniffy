// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeRawManifest 直接落盘 plugin.json 原文,便于构造非法 JSON 等 loadManifest 的错误输入。
func writeRawManifest(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// manifest 缺字段时的兜底:entry→index.js、id→目录名、priority→100。
func TestLoadManifestAppliesDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dir-as-id")
	writeRawManifest(t, dir, `{"name":"仅有名字"}`)

	m, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if m.Entry != "index.js" {
		t.Fatalf("Entry = %q, want index.js", m.Entry)
	}
	if m.ID != "dir-as-id" {
		t.Fatalf("ID = %q, want 目录名 dir-as-id", m.ID)
	}
	if m.Priority != 100 {
		t.Fatalf("Priority = %d, want 100", m.Priority)
	}
	if m.Name != "仅有名字" {
		t.Fatalf("Name = %q", m.Name)
	}
}

// 显式写出的字段不得被默认值覆盖。
func TestLoadManifestKeepsExplicitFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dir-as-id")
	writeRawManifest(t, dir, `{"id":"explicit","entry":"main.js","priority":5,"enabled":true,"whitelist":["*.a.com"]}`)

	m, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if m.ID != "explicit" || m.Entry != "main.js" || m.Priority != 5 || !m.Enabled {
		t.Fatalf("显式字段被默认值覆盖: %+v", m)
	}
	if !reflect.DeepEqual(m.Whitelist, []string{"*.a.com"}) {
		t.Fatalf("Whitelist = %+v", m.Whitelist)
	}
}

func TestLoadManifestErrors(t *testing.T) {
	t.Run("plugin.json 缺失", func(t *testing.T) {
		_, err := loadManifest(t.TempDir())
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("want ErrNotExist, got %v", err)
		}
	})
	t.Run("非法 JSON", func(t *testing.T) {
		dir := t.TempDir()
		writeRawManifest(t, dir, `{"id":"x",`)
		m, err := loadManifest(dir)
		if err == nil {
			t.Fatal("expected json error")
		}
		// 解析失败时返回的必须是零值,不能是打了默认值的半成品。
		if m.ID != "" || m.Entry != "" || m.Priority != 0 {
			t.Fatalf("解析失败仍返回了半成品 manifest: %+v", m)
		}
	})
	t.Run("字段类型不符", func(t *testing.T) {
		dir := t.TempDir()
		writeRawManifest(t, dir, `{"id":"x","priority":"high"}`)
		_, err := loadManifest(dir)
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			t.Fatalf("want UnmarshalTypeError, got %v", err)
		}
	})
}

// saveManifest → loadManifest 往返后字段应完整保留。
func TestSaveManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	orig := Manifest{
		ID: "rt", Name: "往返", Version: "2.0.0", Author: "a", Description: "d",
		Runtime: "js", Entry: "main.js", Enabled: true, Priority: 7,
		Whitelist: []string{"*.a.com"}, Blacklist: []string{"*.b.com"},
		Settings: map[string]any{"n": float64(1), "s": "v"},
		SettingsSchema: []SettingField{{
			Key: "s", Label: "值", Type: "enum",
			Options: []SettingOption{{Value: "v", Label: "V"}},
		}},
	}
	if err := saveManifest(dir, orig); err != nil {
		t.Fatalf("saveManifest: %v", err)
	}
	got, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if !reflect.DeepEqual(got, orig) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, orig)
	}
}

// Settings 里混入不可序列化的值时,saveManifest 必须报错且不得留下半截文件。
func TestSaveManifestMarshalError(t *testing.T) {
	dir := t.TempDir()
	err := saveManifest(dir, Manifest{ID: "x", Settings: map[string]any{"ch": make(chan int)}})
	var unsupported *json.UnsupportedTypeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("want UnsupportedTypeError, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "plugin.json")); !os.IsNotExist(statErr) {
		t.Fatalf("序列化失败却写出了 plugin.json: %v", statErr)
	}
}

func TestAsIntAcceptsJSONAndGoIntegers(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int
	}{
		{"JSON 数字截断小数", float64(3.9), 3},
		{"int", 7, 7},
		{"int64", int64(9), 9},
		{"数字字符串不解析", "12", 0},
		{"bool", true, 0},
		{"nil", nil, 0},
	}
	for _, c := range cases {
		if got := asInt(c.in); got != c.want {
			t.Fatalf("%s: asInt(%v) = %d, want %d", c.name, c.in, got, c.want)
		}
	}
}

func TestAsStringSlice(t *testing.T) {
	// []string 分支原样返回(含空串),不做过滤。
	in := []string{"a", ""}
	if got := asStringSlice(in); !reflect.DeepEqual(got, in) {
		t.Fatalf("[]string 未原样返回: %+v", got)
	}
	// []any 分支只保留非空字符串。
	if got := asStringSlice([]any{"a", "", 1, nil, "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("[]any 过滤结果 = %+v", got)
	}
	if got := asStringSlice([]any{}); got == nil || len(got) != 0 {
		t.Fatalf("空数组应返回非 nil 空切片, got %#v", got)
	}
	for _, v := range []any{nil, "a", 1, map[string]any{"k": "v"}} {
		if got := asStringSlice(v); got != nil {
			t.Fatalf("非数组 %v 应返回 nil, got %+v", v, got)
		}
	}
}

func TestAsSettingSchema(t *testing.T) {
	t.Run("合法数组经 JSON 往返规整", func(t *testing.T) {
		got := asSettingSchema([]any{
			map[string]any{"key": "mode", "type": "enum", "options": []any{
				map[string]any{"value": "a", "label": "A"},
			}},
			map[string]any{"key": "n", "type": "number", "default": float64(3)},
		})
		want := []SettingField{
			{Key: "mode", Type: "enum", Options: []SettingOption{{Value: "a", Label: "A"}}},
			{Key: "n", Type: "number", Default: float64(3)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
	t.Run("nil 与非数组一律降级为 nil", func(t *testing.T) {
		for _, v := range []any{nil, "not-an-array", float64(1), map[string]any{"key": "k"}} {
			if got := asSettingSchema(v); got != nil {
				t.Fatalf("%v 应降级为 nil, got %+v", v, got)
			}
		}
	})
	t.Run("不可序列化的值降级为 nil", func(t *testing.T) {
		if got := asSettingSchema([]any{make(chan int)}); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})
}

// 传输层送来类型不符的字段时,manifestFromMap 应逐个回落到零值而不是 panic。
func TestManifestFromMapIgnoresWrongTypes(t *testing.T) {
	man := manifestFromMap(map[string]any{
		"id":             123,
		"name":           nil,
		"enabled":        "yes",
		"priority":       "5",
		"whitelist":      "*.a.com",
		"settings":       []any{1},
		"settingsSchema": map[string]any{"key": "k"},
	})
	if !reflect.DeepEqual(man, Manifest{}) {
		t.Fatalf("类型不符的字段未回落到零值: %+v", man)
	}
}

// manifestToMap → manifestFromMap 必须无损:Priority 走 int 分支、Whitelist 走 []string 分支。
func TestManifestMapRoundTrip(t *testing.T) {
	orig := Manifest{
		ID: "rt", Name: "N", Version: "1.2.3", Author: "a", Description: "d",
		Runtime: "js", Entry: "main.js", Enabled: true, Priority: 42,
		Whitelist: []string{"*.a.com"}, Blacklist: []string{"*.b.com"},
		Settings: map[string]any{"k": "v"},
		SettingsSchema: []SettingField{{
			Key: "k", Label: "L", Type: "string", Description: "desc",
			Default: "v", Placeholder: "p",
		}},
	}
	if got := manifestFromMap(manifestToMap(orig)); !reflect.DeepEqual(got, orig) {
		t.Fatalf("map 往返丢字段:\n got %+v\nwant %+v", got, orig)
	}
}
