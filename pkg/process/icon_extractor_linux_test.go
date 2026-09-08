// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestParseDesktopEntry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sample.desktop")
	content := "[Other]\nIcon=ignored\n\n[Desktop Entry]\nName=Sample\nExec= /opt/acme/bin/sample --open %U \nIcon= sample-icon \n\n[Desktop Action New]\nIcon=ignored-again\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write desktop entry: %v", err)
	}

	icon, exec := parseDesktopEntry(path)
	if icon != "sample-icon" || exec != "/opt/acme/bin/sample --open %U" {
		t.Fatalf("parseDesktopEntry() = (%q, %q)", icon, exec)
	}
	if icon, exec := parseDesktopEntry(filepath.Join(t.TempDir(), "missing.desktop")); icon != "" || exec != "" {
		t.Fatalf("parseDesktopEntry(missing) = (%q, %q)", icon, exec)
	}
}

func TestFirstExecToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		exec string
		want string
	}{
		{exec: "", want: ""},
		{exec: "firefox %U", want: "firefox"},
		{exec: "  /opt/acme/bin/client --new-window %u", want: "client"},
	}
	for _, tt := range tests {
		if got := firstExecToken(tt.exec); got != tt.want {
			t.Errorf("firstExecToken(%q) = %q, want %q", tt.exec, got, tt.want)
		}
	}
}

func TestLinuxIconDiscoveryAndCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	executable := "/opt/acme/bin/sniffy-test-client"
	iconName := "sniffy-test-icon"
	applications := filepath.Join(home, ".local", "share", "applications")
	themeApps := filepath.Join(home, ".local", "share", "icons", "hicolor", "64x64", "apps")
	for _, dir := range []string{applications, themeApps} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
	}
	desktop := "[Desktop Entry]\nName=Sniffy Test\nExec=" + executable + " %U\nIcon=" + iconName + "\n"
	if err := os.WriteFile(filepath.Join(applications, "sniffy-test.desktop"), []byte(desktop), 0o600); err != nil {
		t.Fatalf("write desktop entry: %v", err)
	}
	// ReadDir 按文件名排序；让缺少 Icon 的同 Exec 条目排在有效条目前，以验证扫描会继续。
	if err := os.MkdirAll(filepath.Join(applications, "nested.desktop"), 0o700); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(applications, "README.txt"), []byte("noise"), 0o600); err != nil {
		t.Fatalf("write noise file: %v", err)
	}
	noIcon := "[Desktop Entry]\nName=No Icon\nExec=" + executable + " %U\n"
	if err := os.WriteFile(filepath.Join(applications, "no-icon.desktop"), []byte(noIcon), 0o600); err != nil {
		t.Fatalf("write icon-less desktop entry: %v", err)
	}
	iconPath := filepath.Join(themeApps, iconName+".png")
	wantData := []byte("fixture icon")
	if err := os.WriteFile(iconPath, wantData, 0o600); err != nil {
		t.Fatalf("write icon: %v", err)
	}

	if got := desktopIconName(executable); got != iconName {
		t.Fatalf("desktopIconName() = %q, want %q", got, iconName)
	}
	path, iconType, ok := resolveIconFile(iconName)
	if !ok || path != iconPath || iconType != "png" {
		t.Fatalf("resolveIconFile() = (%q, %q, %t)", path, iconType, ok)
	}

	extractor := NewIconExtractor()
	first, err := extractor.ExtractIcon(executable)
	if err != nil {
		t.Fatalf("ExtractIcon(): %v", err)
	}
	if !first.HasIcon || first.IconType != "png" || first.IconCategory != "networking" {
		t.Fatalf("ExtractIcon() = %+v", first)
	}
	decoded, err := base64.StdEncoding.DecodeString(first.IconData)
	if err != nil {
		t.Fatalf("decode extracted icon: %v", err)
	}
	if string(decoded) != string(wantData) {
		t.Fatalf("extracted data = %q, want %q", decoded, wantData)
	}

	if err := os.WriteFile(iconPath, []byte("changed"), 0o600); err != nil {
		t.Fatalf("replace icon: %v", err)
	}
	second, err := extractor.ExtractIcon(executable)
	if err != nil {
		t.Fatalf("cached ExtractIcon(): %v", err)
	}
	if second != first {
		t.Fatal("ExtractIcon() did not return the cached icon")
	}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, extractErr := extractor.ExtractIcon(executable)
			if extractErr != nil {
				t.Errorf("concurrent ExtractIcon(): %v", extractErr)
				return
			}
			if got != first {
				t.Errorf("concurrent ExtractIcon() returned an unexpected entry")
			}
		}()
	}
	wg.Wait()
}

func TestLinuxIconFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// 图标查找还会扫描 HOME 之外的系统目录，并按可执行文件基名匹配 Exec。
	// 使用测试专用基名降低命中宿主已安装应用的可能性。
	extractor := NewIconExtractor()
	got, err := extractor.ExtractIcon("/missing/firefox-absent-fixture")
	if err != nil {
		t.Fatalf("ExtractIcon(): %v", err)
	}
	if got.HasIcon || got.IconType != "png" || got.IconSize != "32x32" || got.IconCategory != "browser" {
		t.Fatalf("fallback ExtractIcon() = %+v", got)
	}

	empty, err := extractor.ExtractIcon("")
	if err != nil {
		t.Fatalf("ExtractIcon(empty): %v", err)
	}
	if empty.HasIcon || empty.IconCategory != "application" || empty.IconData == "" {
		t.Fatalf("default ExtractIcon() = %+v", empty)
	}
}

func TestResolveIconFileAbsolutePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svgPath := filepath.Join(dir, "icon.SVG")
	if err := os.WriteFile(svgPath, []byte("<svg/>"), 0o600); err != nil {
		t.Fatalf("write SVG: %v", err)
	}

	path, iconType, ok := resolveIconFile(svgPath)
	if !ok || path != svgPath || iconType != "svg" {
		t.Fatalf("resolveIconFile() = (%q, %q, %t)", path, iconType, ok)
	}
	for _, input := range []string{"", filepath.Join(dir, "missing.png"), filepath.Join(dir, "unsupported.ico"), dir} {
		if path, iconType, ok := resolveIconFile(input); ok || path != "" || iconType != "" {
			t.Errorf("resolveIconFile(%q) = (%q, %q, %t)", input, path, iconType, ok)
		}
	}
}

func TestIconTypeHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantType string
		wantExt  bool
	}{
		{name: "icon.PNG", wantType: "png", wantExt: true},
		{name: "icon.svg", wantType: "svg", wantExt: true},
		{name: "icon.XpM", wantType: "xpm", wantExt: true},
		{name: "icon.ico"},
		{name: "icon"},
	}
	for _, tt := range tests {
		gotType, gotExt := iconTypeOf(tt.name)
		if gotType != tt.wantType || gotExt != tt.wantExt {
			t.Errorf("iconTypeOf(%q) = (%q, %t), want (%q, %t)", tt.name, gotType, gotExt, tt.wantType, tt.wantExt)
		}
	}

	if got := normalizeIconType("svg"); got != "svg" {
		t.Fatalf("normalizeIconType(svg) = %q", got)
	}
	for _, ext := range []string{"png", "xpm", "unknown"} {
		if got := normalizeIconType(ext); got != "png" {
			t.Errorf("normalizeIconType(%q) = %q, want png", ext, got)
		}
	}
}
