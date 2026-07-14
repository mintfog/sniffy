// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package truststore 把根证书装入操作系统级信任库,让本机其它应用直接信任 Sniffy MITM 证书。
// 各平台实现见 truststore_<goos>.go,均需用户在系统授权对话框确认
// (macOS 钥匙串授权 / Windows UAC / Linux pkexec)。
package truststore

import (
	"os"
	"path/filepath"
)

// 稳定错误码,前端据此查 i18n 词条(certs.installErrors.<code>);
// 新增码时需同步 web/src/i18n/locales/ 三套词条。
const (
	CodeCanceled          = "canceled"
	CodeTimeout           = "timeout"        // Detail: 超时秒数
	CodeTrustSettings     = "trust_settings" // Detail: security 原始输出
	CodeKeychain          = "keychain"       // Detail: 底层错误
	CodePkexecMissing     = "pkexec_missing"
	CodeUnsupportedDistro = "unsupported_distro"
	CodeUnsupported       = "unsupported"
)

// Error 表示一类已识别的安装失败,展示文案由前端 i18n 渲染,Go 侧不携带人类可读文本。
// 无法归类的失败(如命令原始 stderr)不用本类型,直接以原文透传。
type Error struct {
	Code   string
	Detail string // 不可翻译的动态内容,作为词条插值,可为空
}

// Error 序列化为 "truststore:<code>[:<detail>]",与前端解析格式约定一致。
func (e *Error) Error() string {
	if e.Detail == "" {
		return "truststore:" + e.Code
	}
	return "truststore:" + e.Code + ":" + e.Detail
}

// ErrUnsupported 在未支持的平台上从 Install 返回。
var ErrUnsupported error = &Error{Code: CodeUnsupported}

// writeTempCert 把 pem 写到独享临时目录里的 sniffy-ca.crt,返回所在目录与完整路径。
// 独立子目录避免与并发调用同名冲突;调用方负责 os.RemoveAll 清理。
func writeTempCert(pem []byte) (dir, path string, err error) {
	dir, err = os.MkdirTemp("", "sniffy-ca-")
	if err != nil {
		return "", "", err
	}
	path = filepath.Join(dir, "sniffy-ca.crt")
	if err := os.WriteFile(path, pem, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	return dir, path, nil
}
