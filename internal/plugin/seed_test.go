// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mintfog/sniffy/internal/pipeline"
)

// dir 为空串时 seedExamples 必须立刻返回:否则 filepath.Join("", "example-add-header")
// 是相对路径,示例插件会被写进进程工作目录。
func TestSeedExamplesWithoutDirDoesNotTouchWorkingDir(t *testing.T) {
	m := NewManager(pipeline.New(nil, nil), "", nil, nil)
	m.seedExamples()

	if _, err := os.Stat("example-add-header"); err == nil {
		_ = os.RemoveAll("example-add-header")
		t.Fatal("seedExamples 在工作目录里创建了示例插件")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat: %v", err)
	}
}

// 内置示例 manifest 必须是合法 JSON、默认禁用,且 settingsSchema 覆盖全部 settings 键,
// 否则配置页会渲染出与脚本读到的值对不上的表单。
func TestExampleManifestIsConsistent(t *testing.T) {
	var man Manifest
	if err := json.Unmarshal([]byte(exampleManifest), &man); err != nil {
		t.Fatalf("exampleManifest 不是合法 JSON: %v", err)
	}
	if man.ID != "example-add-header" || man.Entry != "index.js" || man.Runtime != "js" {
		t.Fatalf("示例 manifest 关键字段异常: %+v", man)
	}
	if man.Enabled {
		t.Fatal("示例插件必须默认禁用")
	}
	keys := make(map[string]bool, len(man.SettingsSchema))
	for _, f := range man.SettingsSchema {
		keys[f.Key] = true
	}
	for k := range man.Settings {
		if !keys[k] {
			t.Fatalf("settings 键 %q 在 settingsSchema 中缺失", k)
		}
	}
}
