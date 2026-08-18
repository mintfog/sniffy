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
// 取不到用户配置目录时回退到当前工作目录下的 .sniffy-fallback/sniffy。
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = ".sniffy-fallback"
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// MkdirAll 不会修复已有目录的权限,而配置目录里有代理凭据。忽略错误:CIFS/exFAT
	// 等不支持 POSIX 权限的挂载点会返回 EPERM,不该因此让应用起不来。
	_ = os.Chmod(dir, 0o700)
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

// CertificatesDir 返回证书持久化目录 <ConfigDir>/certificates 并确保其存在。
// 该目录包含根 CA 和导入证书的私钥，因此新建时只授予当前用户访问权限。
func CertificatesDir() (string, error) {
	cfg, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "certificates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// CacheDir 返回 sniffy 的缓存目录(跨平台),并确保其存在。
//   - Linux:   ~/.cache/sniffy
//   - macOS:   ~/Library/Caches/sniffy
//   - Windows: %LocalAppData%/sniffy
//
// 只存随时可删的可再生产物(如超阈值响应体的落盘副本),不存配置 / 规则 / 证书。
// 内容源自被解密的流量,可能含令牌等敏感信息,故新建时只授予当前用户访问权限。
// 取不到用户缓存目录时回退到当前工作目录下的 .sniffy-fallback/sniffy。
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = ".sniffy-fallback"
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
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
