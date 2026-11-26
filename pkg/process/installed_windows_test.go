// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// createShortcut 用系统的 WScript.Shell 创建一个指向 target、带参数 args 的快捷方式。
func createShortcut(t *testing.T, lnk, target, args string) {
	t.Helper()
	script := "$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:SNIFFY_LNK); $s.TargetPath = $env:SNIFFY_TARGET; $s.Arguments = $env:SNIFFY_ARGS; $s.Save()"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "SNIFFY_LNK="+lnk, "SNIFFY_TARGET="+target, "SNIFFY_ARGS="+args)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("创建快捷方式: %v %s", err, out)
	}
}

// TestStartMenuApp 解析系统创建的真实快捷方式，目录名里带中文；卸载程序、Windows 目录下的系统工具、目标已
// 不存在的不算。
func TestStartMenuApp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "网易云音乐")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "cloudmusic.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	lnk := filepath.Join(dir, "网易云音乐.lnk")
	createShortcut(t, lnk, exe, "")
	windowsDir, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	app, ok := startMenuApp(lnk, windowsDir)
	if !ok || app.Name != "网易云音乐" || !sameFile(app.Path, exe) || app.Entry != "cloudmusic.exe" {
		t.Fatalf("startMenuApp = %+v, %v", app, ok)
	}

	uninstall := filepath.Join(dir, "卸载网易云音乐.lnk")
	createShortcut(t, uninstall, exe, "")
	if _, ok := startMenuApp(uninstall, windowsDir); ok {
		t.Fatal("卸载程序的快捷方式不应列出")
	}
	system := filepath.Join(dir, "命令提示符.lnk")
	createShortcut(t, system, filepath.Join(windowsDir, "System32", "cmd.exe"), "")
	if _, ok := startMenuApp(system, windowsDir); ok {
		t.Fatal("Windows 目录下的系统工具不应列出")
	}
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	if _, ok := startMenuApp(lnk, windowsDir); ok {
		t.Fatal("目标已不存在的快捷方式不应列出")
	}
}

// TestStartMenuAppSquirrel 解析系统创建的 Squirrel 式快捷方式：指向 Update.exe，参数里的程序名带引号。
func TestStartMenuAppSquirrel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Discord")
	discord := filepath.Join(dir, "app-1.0.9200", "Discord.exe")
	if err := os.MkdirAll(filepath.Dir(discord), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, exe := range []string{filepath.Join(dir, "Update.exe"), discord} {
		if err := os.WriteFile(exe, []byte("MZ"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lnk := filepath.Join(dir, "Discord.lnk")
	createShortcut(t, lnk, filepath.Join(dir, "Update.exe"), `--processStart "Discord.exe"`)
	app, ok := startMenuApp(lnk, "")
	if !ok || app.Name != "Discord" || !sameFile(app.Path, discord) || app.Entry != "Discord.exe" {
		t.Fatalf("startMenuApp = %+v, %v", app, ok)
	}
}

// TestInstalledAppsWindows 在本机实际列一次：每项都指向存在的可执行文件，商店应用带解析过的显示名。
func TestInstalledAppsWindows(t *testing.T) {
	start := time.Now()
	apps, err := InstalledApps()
	if err != nil {
		t.Fatal(err)
	}
	store := 0
	for _, a := range apps {
		if _, err := os.Stat(a.Path); err != nil || !strings.EqualFold(filepath.Ext(a.Path), ".exe") || a.Entry != filepath.Base(a.Path) {
			t.Errorf("无效的一项: %+v (%v)", a, err)
		}
		if strings.HasPrefix(a.Name, "ms-resource:") || a.Name == "" {
			t.Errorf("显示名没有解析: %+v", a)
		}
		if strings.Contains(a.Path, `\WindowsApps\`) {
			store++
		}
	}
	t.Logf("耗时 %v，%d 个应用，其中商店应用 %d 个", time.Since(start).Round(time.Millisecond), len(apps), store)
}

// TestExpandEnv：已定义的变量展开，展开后超过 32767 个字符也完整返回；未定义的变量与含 NUL 的串原样返回。
func TestExpandEnv(t *testing.T) {
	t.Setenv("SNIFFY_APPS", `C:\Program Files`)
	long := strings.Repeat("x", 20000)
	t.Setenv("SNIFFY_LONG", long)
	for in, want := range map[string]string{
		`%SNIFFY_APPS%\app.exe`:      `C:\Program Files\app.exe`,
		`%SNIFFY_LONG%%SNIFFY_LONG%`: long + long,
		`%SNIFFY_UNDEFINED%\app.exe`: `%SNIFFY_UNDEFINED%\app.exe`,
		"a\x00b":                     "a\x00b",
		"":                           "",
	} {
		if got := expandEnv(in); got != want {
			t.Errorf("expandEnv(%.40q) = %.40q，期望 %.40q", in, got, want)
		}
	}
}
