// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !windows

package process

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// ProcessIconInfo 进程图标信息。
//
// 该类型在 Windows 上由 icon_extractor_windows.go 单独定义(含相同字段),
// 此处的定义服务于 darwin / linux,二者经互斥的构建标签保证不重复。
type ProcessIconInfo struct {
	IconData     string `json:"iconData"`
	IconType     string `json:"iconType"`
	IconSize     string `json:"iconSize"`
	HasIcon      bool   `json:"hasIcon"`
	IconCategory string `json:"iconCategory"`
}

// iconCategoryForPath 根据可执行文件路径推断图标类别。
// 与 Windows 版 getIconCategory 保持一致,供前端 processIcons 分类着色。
func iconCategoryForPath(executablePath string) string {
	fileName := strings.ToLower(filepath.Base(executablePath))

	switch {
	case containsAny(fileName, "chrome", "chromium", "firefox", "edge", "brave", "opera", "safari", "webkit"):
		return "browser"
	case containsAny(fileName, "code", "visual", "idea", "pycharm", "goland", "webstorm", "clion", "atom", "sublime", "vim", "emacs"):
		return "development"
	case containsAny(fileName, "cmd", "powershell", "pwsh", "bash", "zsh", "fish", "sh", "terminal", "iterm", "konsole", "gnome-terminal", "alacritty"):
		return "terminal"
	case containsAny(fileName, "postman", "insomnia", "curl", "wget", "httpie", "http"):
		return "api-tools"
	case containsAny(fileName, "explorer", "finder", "systemd", "launchd", "svchost", "services", "kernel"):
		return "system"
	case containsAny(fileName, "wireshark", "fiddler", "charles", "tcpdump", "mitmproxy", "sniffy"):
		return "networking"
	default:
		return "application"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// colorIconPNG 依据名称哈希生成一枚确定性的圆形色块图标(base64 PNG),作为兜底。
// 与 Windows 版 createColorIcon 行为一致。
func colorIconPNG(name string) string {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))

	hash := 0
	for _, c := range name {
		hash += int(c)
	}

	colors := []color.RGBA{
		{74, 144, 226, 255},
		{52, 199, 89, 255},
		{255, 59, 48, 255},
		{255, 149, 0, 255},
		{175, 82, 222, 255},
		{255, 204, 0, 255},
		{90, 200, 250, 255},
		{255, 45, 85, 255},
	}
	selected := colors[hash%len(colors)]

	centerX, centerY, radius := 16, 16, 14
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			dx, dy := x-centerX, y-centerY
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, selected)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// fallbackIcon 在无法提取真实图标时,返回基于文件名的色块图标。
func fallbackIcon(executablePath string) *ProcessIconInfo {
	name := strings.ToLower(filepath.Base(executablePath))
	return &ProcessIconInfo{
		IconData:     colorIconPNG(name),
		IconType:     "png",
		IconSize:     "32x32",
		HasIcon:      false,
		IconCategory: iconCategoryForPath(executablePath),
	}
}

// defaultIcon 返回与文件名无关的默认图标。
func defaultIcon() *ProcessIconInfo {
	return &ProcessIconInfo{
		IconData:     colorIconPNG("default"),
		IconType:     "png",
		IconSize:     "32x32",
		HasIcon:      false,
		IconCategory: "application",
	}
}

// encodeIconFile 把磁盘上的图标文件读入并编码为 base64;iconType 取扩展名(png/svg)。
func encodeIconFile(data []byte, iconType string) *ProcessIconInfo {
	return &ProcessIconInfo{
		IconData: base64.StdEncoding.EncodeToString(data),
		IconType: iconType,
		IconSize: "",
		HasIcon:  true,
	}
}

// maxIconFileSize 限制读入的图标文件大小,避免把超大文件编码进 base64。
const maxIconFileSize = 1 << 20 // 1MB

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// readCapped 读取文件内容,超过上限则视为失败。
func readCapped(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxIconFileSize {
		return nil, os.ErrInvalid
	}
	return os.ReadFile(path)
}
