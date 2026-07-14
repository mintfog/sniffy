// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package truststore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// Install 把根证书加入当前用户的登录钥匙串,并设为受信任的根证书。
// 走用户级信任而非系统级:系统级需要 root 且只能弹管理员密码对话框,
// 用户级弹现代授权对话框(支持 Touch ID),对当前用户的抓包场景已足够;
// 多用户机器上需每个用户各自安装。
func Install(pem []byte) error {
	dir, certPath, err := writeTempCert(pem)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	keychain, err := userLoginKeychain()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// 刻意不加 -d,默认走 user 域,弹支持 Touch ID 的授权对话框。
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, certPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// 超时被杀时 CombinedOutput 只报 "signal: killed",以 ctx 错误为准。
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return interpretSecurityErr(err, out)
}

// userLoginKeychain 返回当前用户登录钥匙串的绝对路径。
// 先问 `security default-keychain -d user`(用户可能改过默认),拿不到再回退到标准位置,
// 新旧两种文件名(login.keychain-db / login.keychain)都探测。
func userLoginKeychain() (string, error) {
	if out, err := exec.Command("/usr/bin/security", "default-keychain", "-d", "user").Output(); err == nil {
		if p := parseDefaultKeychain(string(out)); p != "" {
			return p, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		u, uerr := user.Current()
		if uerr != nil {
			return "", &Error{Code: CodeKeychain, Detail: uerr.Error()}
		}
		home = u.HomeDir
	}
	base := filepath.Join(home, "Library", "Keychains")
	for _, name := range []string{"login.keychain-db", "login.keychain"} {
		p := filepath.Join(base, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// 都不存在时仍返回新格式路径,让 security 自行报错。
	return filepath.Join(base, "login.keychain-db"), nil
}

// parseDefaultKeychain 解析 `security default-keychain -d user` 的输出
// (带前导空白与引号的路径),解析失败返回空串。
func parseDefaultKeychain(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	if s == "" || !strings.HasPrefix(s, "/") {
		return ""
	}
	return s
}

// interpretSecurityErr 把 security 命令的失败归类为稳定错误码(*Error),
// 无法识别的输出以原文透传。
func interpretSecurityErr(runErr error, out []byte) error {
	msg := strings.TrimSpace(string(out))
	low := strings.ToLower(msg)

	switch {
	case strings.Contains(msg, "-128"),
		strings.Contains(low, "user canceled"),
		strings.Contains(low, "user cancelled"),
		strings.Contains(low, "authorization was canceled"):
		return &Error{Code: CodeCanceled}
	case strings.Contains(low, "sectrustsettings"),
		strings.Contains(low, "trust settings"):
		return &Error{Code: CodeTrustSettings, Detail: msg}
	}
	if errors.Is(runErr, context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, Detail: "90"}
	}
	if msg == "" {
		return runErr
	}
	return errors.New(msg)
}
