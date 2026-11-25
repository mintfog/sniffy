// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestAppBundles(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"Safari.app/Contents", "Utilities/Terminal.app", "Adobe Photoshop/Adobe Photoshop.app", "Nested/Deeper/Hidden.app", ".app"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := uniqueApps(appBundles([]string{root, filepath.Join(root, "missing")}))
	want := []InstalledApp{
		{Name: "Adobe Photoshop", Path: filepath.Join(root, "Adobe Photoshop", "Adobe Photoshop.app"), Entry: "Adobe Photoshop"},
		{Name: "Safari", Path: filepath.Join(root, "Safari.app"), Entry: "Safari"},
		{Name: "Terminal", Path: filepath.Join(root, "Utilities", "Terminal.app"), Entry: "Terminal"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appBundles = %+v\n期望 %+v", got, want)
	}
}

// TestUniqueApps 按路径去重：指向同一程序的只留一个，条目相同的不同程序各自留下。
func TestUniqueApps(t *testing.T) {
	chrome := filepath.Join(os.TempDir(), "Chrome", "chrome.exe")
	got := uniqueApps([]InstalledApp{
		{Name: "zed", Path: filepath.Join(os.TempDir(), "zed"), Entry: "zed"},
		{Name: "Google Chrome", Path: chrome, Entry: "chrome.exe"},
		{Name: "Chrome 的另一个快捷方式", Path: chrome, Entry: "chrome.exe"},
		{Name: "没有条目", Path: filepath.Join(os.TempDir(), "none")},
		{Name: "游戏甲", Path: filepath.Join(os.TempDir(), "A", "Launcher.exe"), Entry: "Launcher.exe"},
		{Name: "游戏乙", Path: filepath.Join(os.TempDir(), "B", "Launcher.exe"), Entry: "Launcher.exe"},
	})
	names := make([]string, len(got))
	for i, a := range got {
		names[i] = a.Name
	}
	want := []string{"Google Chrome", "zed", "游戏乙", "游戏甲"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("uniqueApps 留下 %q，期望 %q", names, want)
	}
}

func TestSquirrelApp(t *testing.T) {
	root := t.TempDir()
	touch := func(parts ...string) string {
		path := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("MZ"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	discordUpdate := touch("Discord", "Update.exe")
	discord := touch("Discord", "app-1.0.9200", "Discord.exe")
	touch("Discord", "app-1.0.9100", "Discord.exe")
	postmanUpdate := touch("Postman", "Update.exe")
	postman := touch("Postman", "Postman.exe")
	touch("Postman", "app-11.99.0", "Postman.exe")
	other := touch("Other", "Update.exe")
	for _, tc := range []struct {
		name   string
		target string
		args   []string
		want   string
	}{
		{"只在版本目录下，取最后一个版本目录", discordUpdate, []string{"--processStart", "Discord.exe"}, discord},
		{"应用目录下有同名启动程序时取它", postmanUpdate, []string{"--processStartAndWait", "Postman.exe"}, postman},
		{"参数不区分大小写", discordUpdate, []string{"--PROCESSSTART", "Discord.exe", "--process-start-args", "--start-minimized"}, discord},
		{"找不到要启动的程序", other, []string{"--processStart", "Other.exe"}, filepath.Join(root, "Other", "Other.exe")},
		{"不是 Update.exe", discord, []string{"--processStart", "Discord.exe"}, discord},
		{"没有 --processStart", discordUpdate, []string{"--checkForUpdate"}, discordUpdate},
		{"--processStart 后面没有程序", discordUpdate, []string{"--processStart"}, discordUpdate},
		{"要启动的不是同目录下的 exe", discordUpdate, []string{"--processStart", filepath.Join("..", "evil.exe")}, discordUpdate},
	} {
		if got := squirrelApp(tc.target, tc.args); got != tc.want {
			t.Errorf("%s: squirrelApp = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

func TestInstalledAppsUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("这个平台能列出已安装的应用")
	}
	if _, err := InstalledApps(); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("InstalledApps 错误 %v，期望 ErrUnsupported", err)
	}
}
