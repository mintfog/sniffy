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

func TestLangFromEvent(t *testing.T) {
	tests := []struct {
		name string
		data any
		want string
	}{
		{"字符串", "zh-CN", "zh-CN"},
		{"单元素数组", []any{"zh-TW"}, "zh-TW"},
		{"数组首项非字符串", []any{1}, ""},
		{"空数组", []any{}, ""},
		{"其它类型", 1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := langFromEvent(&application.CustomEvent{Data: tt.data}); got != tt.want {
				t.Fatalf("langFromEvent(%#v) = %q，期望 %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestBuildTrayMenuWithoutApplication(t *testing.T) {
	if got := buildTrayMenu(nil, labelsFor("en")); got == nil {
		t.Fatal("托盘菜单不应为空")
	}
	showWindow(nil)
}
