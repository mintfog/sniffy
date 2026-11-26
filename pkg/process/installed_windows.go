// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"encoding/xml"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func installedApps() ([]InstalledApp, error) {
	return append(startMenuApps(), storeApps()...), nil
}

// startMenuApps 列出全体用户与当前用户开始菜单里指向可执行文件的快捷方式，显示名是快捷方式的名字；指向
// Squirrel 的 Update.exe 的取它启动的程序，见 squirrelApp。卸载程序、Windows 目录下的系统工具、指向已
// 不存在的文件的不算。
func startMenuApps() []InstalledApp {
	windowsDir, _ := windows.GetSystemWindowsDirectory()
	var apps []InstalledApp
	for _, folder := range []*windows.KNOWNFOLDERID{windows.FOLDERID_CommonPrograms, windows.FOLDERID_Programs} {
		root, err := windows.KnownFolderPath(folder, 0)
		if err != nil {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".lnk") {
				return nil
			}
			if app, ok := startMenuApp(path, windowsDir); ok {
				apps = append(apps, app)
			}
			return nil
		})
	}
	return apps
}

func startMenuApp(lnk, windowsDir string) (InstalledApp, bool) {
	data, err := os.ReadFile(lnk)
	if err != nil {
		return InstalledApp{}, false
	}
	target, args, err := shortcutTarget(data, decodeACP, expandEnv)
	if err != nil || !strings.EqualFold(filepath.Ext(target), ".exe") {
		return InstalledApp{}, false
	}
	if args != "" {
		argv, _ := windows.DecomposeCommandLine(args)
		target = squirrelApp(target, argv)
	}
	name := strings.TrimSuffix(filepath.Base(lnk), filepath.Ext(lnk))
	if isUninstaller(name, target) || windowsDir != "" && isUnder(target, windowsDir) {
		return InstalledApp{}, false
	}
	if _, err := os.Stat(target); err != nil {
		return InstalledApp{}, false
	}
	return InstalledApp{Name: name, Path: target, Entry: filepath.Base(target)}, true
}

func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}

func isUninstaller(name, target string) bool {
	name, exe := strings.ToLower(name), strings.ToLower(filepath.Base(target))
	return strings.Contains(exe, "unins") || strings.Contains(name, "uninstall") || strings.Contains(name, "卸载")
}

// appxPackagesKey 下每个子键是当前用户安装的一个商店应用包，PackageRootFolder 是它的安装目录。
const appxPackagesKey = `Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages`

// appxManifest 是 AppxManifest.xml 里用到的部分。标签不带命名空间，各版本清单的命名空间都能匹配。
type appxManifest struct {
	Identity struct {
		Name string `xml:"Name,attr"`
	} `xml:"Identity"`
	DisplayName  string `xml:"Properties>DisplayName"`
	Applications []struct {
		Executable string `xml:"Executable,attr"`
		Visual     struct {
			DisplayName  string `xml:"DisplayName,attr"`
			AppListEntry string `xml:"AppListEntry,attr"`
		} `xml:"VisualElements"`
	} `xml:"Applications>Application"`
}

// storeApps 列出当前用户的商店应用（MSIX / UWP）里在开始菜单显示的那些。运行库、资源包没有带可执行文件的
// 应用，不会列出；同一应用的旧版本留在注册表里时，可执行文件已经不在的跳过。
func storeApps() []InstalledApp {
	packages, err := registry.OpenKey(registry.CURRENT_USER, appxPackagesKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer packages.Close()
	fullNames, _ := packages.ReadSubKeyNames(-1)
	var apps []InstalledApp
	for _, fullName := range fullNames {
		apps = append(apps, packageApps(packages, fullName)...)
	}
	return apps
}

func packageApps(packages registry.Key, fullName string) []InstalledApp {
	k, err := registry.OpenKey(packages, fullName, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	root, _, err := k.GetStringValue("PackageRootFolder")
	k.Close()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, "AppxManifest.xml"))
	if err != nil {
		return nil
	}
	var m appxManifest
	if xml.Unmarshal(data, &m) != nil {
		return nil
	}
	var apps []InstalledApp
	for _, a := range m.Applications {
		if a.Executable == "" || strings.EqualFold(a.Visual.AppListEntry, "none") {
			continue
		}
		path := filepath.Join(root, a.Executable)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		name := resourceString(fullName, m.Identity.Name, a.Visual.DisplayName)
		if name == "" {
			name = resourceString(fullName, m.Identity.Name, m.DisplayName)
		}
		if name == "" {
			name = m.Identity.Name
		}
		apps = append(apps, InstalledApp{Name: name, Path: path, Entry: filepath.Base(path)})
	}
	return apps
}

var shLoadIndirectString = windows.NewLazySystemDLL("shlwapi.dll").NewProc("SHLoadIndirectString")

// resourceString 把清单里 ms-resource: 开头的字符串按包的资源解析成显示文本，其余原样返回，解析不了时为空。
// 资源名不带路径时在 Resources 下。
func resourceString(fullName, packageName, value string) string {
	ref, ok := strings.CutPrefix(value, "ms-resource:")
	if !ok {
		return value
	}
	var uri string
	switch {
	case strings.HasPrefix(ref, "//"):
		uri = "ms-resource:" + ref
	case strings.HasPrefix(ref, "/"):
		uri = "ms-resource://" + packageName + ref
	case strings.Contains(ref, "/"):
		uri = "ms-resource://" + packageName + "/" + ref
	default:
		uri = "ms-resource://" + packageName + "/Resources/" + ref
	}
	source, err := windows.UTF16PtrFromString("@{" + fullName + "?" + uri + "}")
	if err != nil {
		return ""
	}
	var buf [512]uint16
	if hr, _, _ := shLoadIndirectString.Call(uintptr(unsafe.Pointer(source)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); hr != 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

// cpACP 是 MultiByteToWideChar 的 CP_ACP：系统 ANSI 代码页。
const cpACP = 0

// decodeACP 按系统 ANSI 代码页解码。
func decodeACP(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n, err := windows.MultiByteToWideChar(cpACP, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n == 0 {
		return string(b)
	}
	buf := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cpACP, 0, &b[0], int32(len(b)), &buf[0], n); err != nil {
		return string(b)
	}
	return windows.UTF16ToString(buf)
}

// expandEnv 展开 s 里的环境变量，展开不了时原样返回。
func expandEnv(s string) string {
	if expanded, err := registry.ExpandString(s); err == nil {
		return expanded
	}
	return s
}
