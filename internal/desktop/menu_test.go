// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestMenuRole(t *testing.T) {
	tests := []struct {
		input string
		want  application.Role
	}{
		{"about", application.About},
		{"quit", application.Quit},
		{"hide", application.Hide},
		{"hideOthers", application.HideOthers},
		{"showAll", application.ShowAll},
		{"services", application.ServicesMenu},
		{"undo", application.Undo},
		{"redo", application.Redo},
		{"cut", application.Cut},
		{"copy", application.Copy},
		{"paste", application.Paste},
		{"selectAll", application.SelectAll},
		{"minimise", application.Minimise},
		{"zoom", application.Zoom},
		{"close", application.CloseWindow},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := menuRole(tt.input)
			if !ok || got != tt.want {
				t.Fatalf("menuRole(%q) = (%v, %v)，期望 (%v, true)", tt.input, got, ok, tt.want)
			}
		})
	}

	if got, ok := menuRole("unknown"); ok || got != application.NoRole {
		t.Fatalf("未知角色 = (%v, %v)，期望 (NoRole, false)", got, ok)
	}
}

func TestEditMenuDetectionAndRenderedCount(t *testing.T) {
	if !isEditMenu(menuNode{Items: []menuNode{{Kind: "role", Role: "copy"}}}) {
		t.Fatal("含剪贴板角色的菜单应被识别为编辑菜单")
	}
	if isEditMenu(menuNode{Items: []menuNode{{Kind: "role", Role: "quit"}, {Kind: "item"}}}) {
		t.Fatal("不含剪贴板角色的菜单不应被识别为编辑菜单")
	}

	items := []menuNode{
		{Kind: "item"},
		{Kind: "separator"},
		{Kind: "role", Role: "copy"},
		{Kind: "role", Role: "unknown"},
		{Kind: "submenu"},
	}
	if got := renderedCount(items); got != 4 {
		t.Fatalf("renderedCount() = %d，期望 4", got)
	}
}

func TestBuildNativeMenus(t *testing.T) {
	checked := true
	root := application.NewMenu()
	addMenuNode(root, menuNode{Kind: "separator"})
	addMenuNode(root, menuNode{Kind: "role", Role: "unknown"})
	addMenuNode(root, menuNode{
		Kind:  "submenu",
		Label: "文件",
		Items: []menuNode{{Kind: "item", ID: "save", Label: "保存", Disabled: true}},
	})
	addMenuNode(root, menuNode{Kind: "item", Label: "勾选", Checked: &checked})
	addMenuNode(root, menuNode{Kind: "item", Label: "普通"})

	(&Bridge{}).SetMenu(nil)
}
