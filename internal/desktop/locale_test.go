// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import "testing"

func TestNormalizeLocale(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"zh", "zh-Hans"},
		{"zh-CN", "zh-Hans"},
		{"zh_Hans_SG.UTF-8", "zh-Hans"},
		{"zh-Hant", "zh-Hant"},
		{"zh-TW", "zh-Hant"},
		{"ZH_hk.UTF-8", "zh-Hant"},
		{"zh-MO", "zh-Hant"},
		{"en-US", "en"},
		{"az-TW", "en"},
		{"", "en"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeLocale(tt.input); got != tt.want {
				t.Fatalf("normalizeLocale(%q) = %q，期望 %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLabelsFor(t *testing.T) {
	tests := []struct {
		lang            string
		wantShow        string
		wantEdit        string
		wantDescription string
	}{
		{"en", "Show Sniffy", "Edit", "HTTP/HTTPS capture & proxy tool"},
		{"zh-Hant", "顯示 Sniffy", "編輯", "HTTP/HTTPS 封包擷取代理工具"},
		{"zh-Hans", "显示 Sniffy", "编辑", "HTTP/HTTPS 抓包代理工具"},
		{"unsupported", "显示 Sniffy", "编辑", "HTTP/HTTPS 抓包代理工具"},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			got := labelsFor(tt.lang)
			if got.show != tt.wantShow || got.edit != tt.wantEdit || got.description != tt.wantDescription {
				t.Fatalf("labelsFor(%q) = show:%q edit:%q description:%q", tt.lang, got.show, got.edit, got.description)
			}
			if got.about == "" || got.quit == "" || got.closeWindow == "" {
				t.Fatal("占位菜单的必要标签不应为空")
			}
		})
	}
}

func TestUILangUsesPreferredLocale(t *testing.T) {
	t.Setenv("LC_ALL", "zh-TW.UTF-8")
	if got := uiLang(); got != "zh-Hant" {
		t.Fatalf("uiLang() = %q，期望 zh-Hant", got)
	}
}
