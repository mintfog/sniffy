// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package truststore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// Install 把根证书加入当前用户的登录钥匙串,并设为受信任的根证书。
//
// 走用户级信任(user-domain)而不是系统级:
//   - 系统级(-d admin + /Library/Keychains/System.keychain)需要 root,
//     经典做法是走 osascript "with administrator privileges"——本质是已废弃的
//     AuthorizationExecuteWithPrivileges,只能弹**管理员密码**,不支持 Touch ID,
//     现代 macOS 上也常在密码输入后仍报错。
//   - 用户级(登录钥匙串 + 默认 user 域)由 security 命令直接触发
//     SecTrustSettingsSetTrustSettings,弹的是现代 SecurityAgent 对话框,
//     启用了 Touch ID 的机器上直接刷指纹即可。
//
// 对 Sniffy 的抓包场景足够:当前用户下 Safari/Chrome/Firefox/curl 等都认此信任。
// 多用户机器上需要每个用户各自安装。
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

	// -r trustRoot: 作为根证书信任;-k <keychain>: 装入指定钥匙串。
	// 不加 -d,默认走 user 域,弹现代授权对话框(支持 Touch ID)。
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, certPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	return interpretSecurityErr(err, out)
}

// userLoginKeychain 返回当前用户登录钥匙串的绝对路径。
// 先问 `security default-keychain -d user`(用户可能改过默认),拿不到再回退到标准位置。
// 高版本 macOS 默认为 login.keychain-db(SQLite);从旧系统迁移过来的可能仍是 login.keychain,
// 两个都探测,取存在的那个。
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
			return "", fmt.Errorf("无法定位登录钥匙串: %w", uerr)
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
	// 两个都不存在(极罕见,可能新装 macOS 首次运行前),仍返回新格式路径让 security 自行报错。
	return filepath.Join(base, "login.keychain-db"), nil
}

// parseDefaultKeychain 解析 `security default-keychain -d user` 的输出:
// 形如 `    "/Users/foo/Library/Keychains/login.keychain-db"`,前有空白后带引号。
// 解析失败返回空串。
func parseDefaultKeychain(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	if s == "" || !strings.HasPrefix(s, "/") {
		return ""
	}
	return s
}

// interpretSecurityErr 把 security 命令的失败信号翻译成中文用户可读的错误。
// 优先识别几个稳定的字符串锚点(SecTrustSettingsSetTrustSettings/User canceled/-128 等),
// 其它情况保留 stderr 尾部原文,避免"neutral error"式无信息。
func interpretSecurityErr(runErr error, out []byte) error {
	msg := strings.TrimSpace(string(out))
	low := strings.ToLower(msg)

	switch {
	case strings.Contains(msg, "-128"),
		strings.Contains(low, "user canceled"),
		strings.Contains(low, "user cancelled"),
		strings.Contains(low, "authorization was canceled"):
		return errors.New("已取消授权")
	case strings.Contains(low, "sectrustsettings"),
		strings.Contains(low, "trust settings"):
		if msg != "" {
			return fmt.Errorf("修改信任设置失败: %s", msg)
		}
		return errors.New("修改信任设置失败")
	}
	if errors.Is(runErr, context.DeadlineExceeded) {
		return errors.New("授权对话框超时(90 秒未响应)")
	}
	if msg == "" {
		return runErr
	}
	return fmt.Errorf("%s", msg)
}
