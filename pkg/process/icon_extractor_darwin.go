// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// IconExtractor macOS 图标提取器。
//
// 提取链路:可执行文件 → 所属 .app 包 → Info.plist 的 CFBundleIconFile → Resources 下的
// .icns → 经系统自带 sips 转成 PNG → base64。失败则回退到基于文件名的色块图标。
// 全程仅调用系统命令(plutil/sips),不依赖 cgo。
type IconExtractor struct {
	mu    sync.Mutex
	cache map[string]*ProcessIconInfo
}

// NewIconExtractor 创建 macOS 图标提取器。
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
	appDir := findAppBundle(executablePath)
	if appDir == "" {
		return fallbackIcon(executablePath)
	}

	icnsPath := locateICNS(appDir)
	if icnsPath == "" {
		return fallbackIcon(executablePath)
	}

	pngData, err := icnsToPNG(icnsPath)
	if err != nil || len(pngData) == 0 {
		return fallbackIcon(executablePath)
	}

	info := encodeIconFile(pngData, "png")
	info.IconCategory = iconCategoryForPath(executablePath)
	return info
}

// findAppBundle 从可执行文件路径向上查找 .app 包目录。
func findAppBundle(executablePath string) string {
	dir := executablePath
	for {
		if strings.HasSuffix(dir, ".app") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "/" || parent == "." {
			return ""
		}
		dir = parent
	}
}

// locateICNS 读取 Info.plist 的 CFBundleIconFile 并定位 Resources 下的 .icns 文件。
func locateICNS(appDir string) string {
	resources := filepath.Join(appDir, "Contents", "Resources")
	plist := filepath.Join(appDir, "Contents", "Info.plist")

	iconFile := plistString(plist, "CFBundleIconFile")
	if iconFile != "" {
		// CFBundleIconFile 可能带或不带 .icns 后缀。
		candidates := []string{iconFile}
		if filepath.Ext(iconFile) == "" {
			candidates = append(candidates, iconFile+".icns")
		}
		for _, c := range candidates {
			p := filepath.Join(resources, c)
			if fileExists(p) {
				return p
			}
		}
	}

	// 回退:Resources 下任取一个 .icns。
	if entries, err := os.ReadDir(resources); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".icns") {
				return filepath.Join(resources, e.Name())
			}
		}
	}
	return ""
}

// plistString 用 plutil 抽取 plist 中某键的字符串值(兼容二进制与 XML plist)。
func plistString(plistPath, key string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "plutil", "-extract", key, "raw", "-o", "-", plistPath).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// icnsToPNG 调用 sips 把 .icns 转成 PNG 并读回字节。
func icnsToPNG(icnsPath string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "sniffy-icon-*.png")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "sips", "-s", "format", "png", "-Z", "64", icnsPath, "--out", tmpPath).Run(); err != nil {
		return nil, err
	}
	return readCapped(tmpPath)
}
