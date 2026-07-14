// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package truststore 把根证书装入操作系统级信任库,让本机其它应用直接信任 Sniffy MITM 证书。
// 各平台实现见 truststore_<goos>.go,均依赖系统授权对话框提权
// (macOS osascript / Windows UAC / Linux pkexec)。
package truststore

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrUnsupported 在未支持的平台上从 Install 返回。
var ErrUnsupported = errors.New("当前平台不支持自动安装根证书,请按界面引导手动安装")

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
