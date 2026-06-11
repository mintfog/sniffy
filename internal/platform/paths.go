// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package platform 收敛与操作系统相关的细节(配置/数据目录、证书安装等)。
package platform

import (
	"os"
	"path/filepath"
)

const appDirName = "sniffy"

// ConfigDir 返回 sniffy 的用户配置目录(跨平台),并确保其存在。
//   - Linux:   ~/.config/sniffy
//   - macOS:   ~/Library/Application Support/sniffy
//   - Windows: %AppData%/sniffy
//
// 取不到用户配置目录时回退到当前工作目录下的 .sniffy。
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = ".sniffy-fallback"
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// PluginsDir 返回用户插件目录 <ConfigDir>/plugins 并确保其存在。
func PluginsDir() (string, error) {
	cfg, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// DataDir 返回 sniffy 的用户数据目录(跨平台),并确保其存在。
func DataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = ".sniffy-fallback"
	}
	dir := filepath.Join(base, appDirName, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// LogsDir 返回 sniffy 的日志目录 <ConfigDir>/logs 并确保其存在。
func LogsDir() (string, error) {
	cfg, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
