// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Manifest 是插件目录下 plugin.json 的结构。
type Manifest struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Author      string         `json:"author"`
	Description string         `json:"description"`
	Runtime     string         `json:"runtime"` // 目前仅 "js"
	Entry       string         `json:"entry"`   // 脚本文件名,默认 index.js
	Enabled     bool           `json:"enabled"`
	Priority    int            `json:"priority"`
	Whitelist   []string       `json:"whitelist,omitempty"`
	Blacklist   []string       `json:"blacklist,omitempty"`
	Settings    map[string]any `json:"settings,omitempty"`
}

func loadManifest(dir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Entry == "" {
		m.Entry = "index.js"
	}
	if m.ID == "" {
		m.ID = filepath.Base(dir)
	}
	if m.Priority == 0 {
		m.Priority = 100
	}
	return m, nil
}

func saveManifest(dir string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "plugin.json"), data, 0o644)
}

// manifestFromMap 把前端/传输层传入的 manifest 字段宽松解析为 Manifest。
func manifestFromMap(m map[string]any) Manifest {
	return Manifest{
		ID:          asString(m["id"]),
		Name:        asString(m["name"]),
		Version:     asString(m["version"]),
		Author:      asString(m["author"]),
		Description: asString(m["description"]),
		Runtime:     asString(m["runtime"]),
		Entry:       asString(m["entry"]),
		Enabled:     asBool(m["enabled"]),
		Priority:    asInt(m["priority"]),
		Whitelist:   asStringSlice(m["whitelist"]),
		Blacklist:   asStringSlice(m["blacklist"]),
		Settings:    asMap(m["settings"]),
	}
}

// manifestToMap 把 Manifest 转回 map,供创建后回传 UI。
func manifestToMap(m Manifest) map[string]any {
	return map[string]any{
		"id":          m.ID,
		"name":        m.Name,
		"version":     m.Version,
		"author":      m.Author,
		"description": m.Description,
		"runtime":     m.Runtime,
		"entry":       m.Entry,
		"enabled":     m.Enabled,
		"priority":    m.Priority,
		"whitelist":   m.Whitelist,
		"blacklist":   m.Blacklist,
		"settings":    m.Settings,
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// asInt 兼容 JSON 数字(float64)与各整型。
func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func asStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
