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
