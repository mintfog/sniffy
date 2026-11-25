// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var (
	linuxAppRules   = appRules{separators: "/"}
	darwinAppRules  = appRules{separators: "/", foldPaths: true, trimSuffix: ".app", appBundles: true}
	windowsAppRules = appRules{separators: `\/`, foldPaths: true, trimSuffix: ".exe"}
)

func noSymlinks(string) (string, error) { return "", errors.New("不是符号链接") }

func TestNormalizeAppEntries(t *testing.T) {
	dir, sep := t.TempDir(), string(filepath.Separator)
	// filepath.Join 自己就会 Clean，未规范化的路径要拼出来。
	raw := dir + sep + "bin" + sep + ".." + sep + "curl" + sep
	clean := filepath.Join(dir, "curl")
	if raw == clean {
		t.Fatal("构造的路径已经是规范形式")
	}
	got, err := NormalizeAppEntries([]string{" chrome ", "", "\t", raw, "chrome", clean})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"chrome", clean}; !slices.Equal(got, want) {
		t.Fatalf("规范化结果 %q，期望 %q", got, want)
	}
	if got, err := NormalizeAppEntries(nil); err != nil || len(got) != 0 {
		t.Fatalf("空名单: %q, %v", got, err)
	}

	for _, bad := range []string{"bin/curl", "./curl", ".", "..", "curl\x00", "a\nb", strings.Repeat("a", maxAppEntryLen+1)} {
		if _, err := NormalizeAppEntries([]string{"chrome", bad}); err == nil {
			t.Errorf("%q 应当被拒绝", bad)
		}
	}

	// 反斜杠只在 Windows 上是路径分隔符，其它平台当成程序名的话永远匹配不上；Windows 上斜杠开头、
	// 不带盘符的也不是绝对路径（Git Bash 的 /c/Users/…）。
	windowsPath, unixPath := `C:\Tools\app.exe`, "/c/Users/me/app.exe"
	_, windowsErr := NormalizeAppEntries([]string{windowsPath})
	_, unixErr := NormalizeAppEntries([]string{unixPath})
	if onWindows := runtime.GOOS == "windows"; (windowsErr == nil) != onWindows || (unixErr == nil) == onWindows {
		t.Errorf("%s 上 %q: %v；%q: %v", runtime.GOOS, windowsPath, windowsErr, unixPath, unixErr)
	}
	if runtime.GOOS == "windows" {
		if _, err := NormalizeAppEntries([]string{"C:app.exe"}); err == nil {
			t.Error("盘符相对路径 C:app.exe 应当被拒绝")
		}
	}

	many := make([]string, MaxAppEntries+1)
	for i := range many {
		many[i] = fmt.Sprintf("app%d", i)
	}
	if _, err := NormalizeAppEntries(many[:MaxAppEntries]); err != nil {
		t.Fatalf("恰好 %d 项: %v", MaxAppEntries, err)
	}
	if _, err := NormalizeAppEntries(many); err == nil {
		t.Fatalf("%d 项应当超出上限", len(many))
	}
}

func TestAppSetMatch(t *testing.T) {
	type probe struct {
		path, name string
		want       bool
	}
	cases := []struct {
		name    string
		rules   appRules
		entries []string
		probes  []probe
	}{
		{
			name:    "Linux 程序名比较文件名与 comm，不区分大小写",
			rules:   linuxAppRules,
			entries: []string{"Chrome", "python3"},
			probes: []probe{
				{path: "/opt/google/chrome/chrome", want: true},
				{path: "/usr/bin/python3.12", name: "python3", want: true},
				{path: "/usr/bin/python3.12", name: "python3.12"},
				{path: "/usr/bin/chromium"},
				{path: "", name: "CHROME", want: true},
				{path: "/opt/chrome/helper"},
			},
		},
		{
			name:    "Linux 路径选中文件本身与目录下的全部程序，区分大小写",
			rules:   linuxAppRules,
			entries: []string{"/opt/app", "/usr/bin/curl", "/srv/tools/"},
			probes: []probe{
				{path: "/opt/app", want: true},
				{path: "/opt/app/bin/run", want: true},
				{path: "/opt/application/run"},
				{path: "/OPT/app/run"},
				{path: "/usr/bin/curl", want: true},
				{path: "/usr/bin/curl2"},
				{path: "/srv/tools/x", want: true},
				{path: "", name: "curl"},
			},
		},
		{
			name:    "根目录选中全部",
			rules:   linuxAppRules,
			entries: []string{"/"},
			probes:  []probe{{path: "/usr/bin/curl", want: true}, {path: "", name: "curl"}},
		},
		{
			name:    "Windows 程序名可以省略 .exe，路径不区分大小写",
			rules:   windowsAppRules,
			entries: []string{"chrome", "Code.EXE", `C:\Program Files\Mozilla Firefox`},
			probes: []probe{
				{path: `C:\Program Files\Google\Chrome\Application\chrome.exe`, want: true},
				{path: `C:\Users\u\AppData\Local\Programs\Microsoft VS Code\code.exe`, want: true},
				{path: `c:\program files\mozilla firefox\firefox.exe`, want: true},
				{path: `C:\Program Files\Mozilla Firefox Beta\firefox.exe`},
				{path: "", name: "chrome.exe", want: true},
				{path: `C:\Windows\System32\svchost.exe`},
			},
		},
		{
			name:    "macOS 程序名覆盖最外层 .app 包里的全部程序",
			rules:   darwinAppRules,
			entries: []string{"Google Chrome", "/Applications/Safari.app"},
			probes: []probe{
				{path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", want: true},
				{path: "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/131/Helpers/Google Chrome Helper.app/Contents/MacOS/Google Chrome Helper", want: true},
				{path: "/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary"},
				{path: "/applications/safari.app/contents/macos/safari", want: true},
				{path: "/usr/bin/curl"},
			},
		},
		{
			name:    "macOS 程序名写成带 .app 的包名同样生效",
			rules:   darwinAppRules,
			entries: []string{"Postman.app", "Google Chrome.APP"},
			probes: []probe{
				{path: "/Applications/Postman.app/Contents/MacOS/Postman", want: true},
				{path: "/Applications/Postman.app/Contents/Frameworks/Postman Helper.app/Contents/MacOS/Postman Helper", want: true},
				{path: "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/131/Helpers/Google Chrome Helper.app/Contents/MacOS/Google Chrome Helper", want: true},
				{path: "/Applications/Postman Canary.app/Contents/MacOS/Postman Canary"},
			},
		},
		{
			name:    "其它平台不认 .app 包与 .exe",
			rules:   linuxAppRules,
			entries: []string{"Google Chrome", "curl.exe"},
			probes: []probe{
				{path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome Helper"},
				{path: "/usr/bin/curl"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newAppSet(tc.entries, tc.rules, noSymlinks)
			for _, p := range tc.probes {
				if got := s.Match(p.path, p.name); got != p.want {
					t.Errorf("Match(%q, %q) = %v，期望 %v", p.path, p.name, got, p.want)
				}
			}
		})
	}
}

func TestAppSetResolvesSymlinks(t *testing.T) {
	s := newAppSet([]string{"/usr/bin/python3"}, linuxAppRules, func(p string) (string, error) {
		return "/usr/bin/python3.12", nil
	})
	for _, path := range []string{"/usr/bin/python3", "/usr/bin/python3.12"} {
		if !s.Match(path, "") {
			t.Errorf("%s 应当被选中", path)
		}
	}
	// 真实文件系统：经符号链接的目录条目同样选中解析后目录里的程序。
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	// Windows 上创建符号链接需要开发者模式或管理员权限。
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if !NewAppSet([]string{link}).Match(filepath.Join(resolved, "app"), "") {
		t.Fatalf("%s 经符号链接指向 %s，其中的程序应当被选中", link, resolved)
	}
}

func TestAppSetEmpty(t *testing.T) {
	var nilSet *AppSet
	for _, s := range []*AppSet{nilSet, NewAppSet(nil)} {
		if !s.Empty() || s.Match("/usr/bin/curl", "curl") {
			t.Fatalf("空名单不应选中任何进程")
		}
	}
	if NewAppSet([]string{"curl"}).Empty() {
		t.Fatal("非空名单报告为空")
	}
}
