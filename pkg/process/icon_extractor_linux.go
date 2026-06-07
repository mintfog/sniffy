// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// IconExtractor Linux 图标提取器。
//
// 提取链路:可执行文件 → 匹配的 .desktop 文件 → Icon= 名称 → 图标主题/pixmaps 中的
// 实际文件 → base64(png/svg)。任一环节失败则回退到基于文件名的色块图标(best-effort)。
type IconExtractor struct {
	mu    sync.Mutex
	cache map[string]*ProcessIconInfo
}

// NewIconExtractor 创建 Linux 图标提取器。
func NewIconExtractor() *IconExtractor {
	return &IconExtractor{cache: make(map[string]*ProcessIconInfo)}
}

// ExtractIcon 尽力从可执行文件解析出应用图标。
func (ie *IconExtractor) ExtractIcon(executablePath string) (*ProcessIconInfo, error) {
	if executablePath == "" {
		return defaultIcon(), nil
	}

	ie.mu.Lock()
	if cached, ok := ie.cache[executablePath]; ok {
		ie.mu.Unlock()
		return cached, nil
	}
	ie.mu.Unlock()

	icon := ie.extract(executablePath)

	ie.mu.Lock()
	ie.cache[executablePath] = icon
	ie.mu.Unlock()
	return icon, nil
}

func (ie *IconExtractor) extract(executablePath string) *ProcessIconInfo {
	iconName := desktopIconName(executablePath)
	if iconName == "" {
		// 退而用可执行文件基名作为图标名再试一次。
		iconName = strings.TrimSuffix(filepath.Base(executablePath), filepath.Ext(executablePath))
	}

	if path, iconType, ok := resolveIconFile(iconName); ok {
		if data, err := readCapped(path); err == nil {
			info := encodeIconFile(data, iconType)
			info.IconCategory = iconCategoryForPath(executablePath)
			return info
		}
	}

	return fallbackIcon(executablePath)
}

// desktopDirs 返回标准 .desktop 搜索目录(含 XDG_DATA_DIRS 与用户目录)。
func desktopDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local/share/applications"))
	}
	dirs = append(dirs,
		"/usr/share/applications",
		"/usr/local/share/applications",
		"/var/lib/flatpak/exports/share/applications",
		"/var/lib/snapd/desktop/applications",
	)
	return dirs
}

// desktopIconName 在 .desktop 文件中查找 Exec 指向给定可执行文件的条目,返回其 Icon= 值。
func desktopIconName(executablePath string) string {
	base := filepath.Base(executablePath)
	for _, dir := range desktopDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			icon, exec := parseDesktopEntry(filepath.Join(dir, e.Name()))
			if icon == "" {
				continue
			}
			// Exec 第一段(去掉参数与路径)与目标可执行文件基名一致即认为匹配。
			if execBase := firstExecToken(exec); execBase == base || execBase == executablePath {
				return icon
			}
		}
	}
	return ""
}

// parseDesktopEntry 读取 .desktop 的 [Desktop Entry] 段中的 Icon 与 Exec。
func parseDesktopEntry(path string) (icon, exec string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	inEntry := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		switch {
		case strings.HasPrefix(line, "Icon="):
			icon = strings.TrimSpace(strings.TrimPrefix(line, "Icon="))
		case strings.HasPrefix(line, "Exec="):
			exec = strings.TrimSpace(strings.TrimPrefix(line, "Exec="))
		}
	}
	return icon, exec
}

// firstExecToken 取 Exec 行的第一个 token 的基名(忽略 %f/%U 之类的占位符与路径)。
func firstExecToken(exec string) string {
	fields := strings.Fields(exec)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// iconThemeDirs 返回图标主题与 pixmaps 的搜索根目录。
func iconThemeDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local/share/icons"), filepath.Join(home, ".icons"))
	}
	dirs = append(dirs, "/usr/share/icons", "/usr/local/share/icons")
	return dirs
}

// iconThemeSizes 优先尝试较大的位图尺寸。
var iconThemeSizes = []string{"512x512", "256x256", "128x128", "96x96", "64x64", "48x48", "32x32", "24x24", "16x16"}

// resolveIconFile 把图标名(可能是绝对路径或主题名)解析为磁盘上的实际文件。
func resolveIconFile(iconName string) (path, iconType string, ok bool) {
	if iconName == "" {
		return "", "", false
	}

	// 绝对路径直接使用。
	if filepath.IsAbs(iconName) {
		if t, ok := iconTypeOf(iconName); ok && fileExists(iconName) {
			return iconName, t, true
		}
	}

	// 名称已带后缀(png/svg)时,先在 pixmaps 直接找。
	if t, hasExt := iconTypeOf(iconName); hasExt {
		for _, base := range []string{"/usr/share/pixmaps", "/usr/local/share/pixmaps"} {
			p := filepath.Join(base, iconName)
			if fileExists(p) {
				return p, t, true
			}
		}
	}

	name := strings.TrimSuffix(iconName, filepath.Ext(iconName))

	// pixmaps(无尺寸层级)。
	for _, base := range []string{"/usr/share/pixmaps", "/usr/local/share/pixmaps"} {
		for _, ext := range []string{"png", "svg", "xpm"} {
			p := filepath.Join(base, name+"."+ext)
			if fileExists(p) {
				return p, normalizeIconType(ext), true
			}
		}
	}

	// 图标主题:hicolor 各尺寸的 apps/,以及 scalable 的 svg。
	for _, root := range iconThemeDirs() {
		for _, theme := range []string{"hicolor", "Adwaita", "gnome", "breeze"} {
			for _, size := range iconThemeSizes {
				p := filepath.Join(root, theme, size, "apps", name+".png")
				if fileExists(p) {
					return p, "png", true
				}
			}
			p := filepath.Join(root, theme, "scalable", "apps", name+".svg")
			if fileExists(p) {
				return p, "svg", true
			}
		}
	}

	return "", "", false
}

// iconTypeOf 依据扩展名判断图标类型;hasExt 表示是否带可识别后缀。
func iconTypeOf(name string) (iconType string, hasExt bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "png", true
	case ".svg":
		return "svg", true
	case ".xpm":
		return "xpm", true
	default:
		return "", false
	}
}

func normalizeIconType(ext string) string {
	if ext == "svg" {
		return "svg"
	}
	return "png"
}
