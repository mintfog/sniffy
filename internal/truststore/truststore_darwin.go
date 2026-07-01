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
	"strings"
	"time"
)

// Install 用 osascript 触发系统密码对话框,以管理员权限调 security 把证书装入系统钥匙串
// 并标记为受信任的根证书。
func Install(pem []byte) error {
	dir, certPath, err := writeTempCert(pem)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// `quoted form of` 让 AppleScript 给出 shell 安全的引号串,防 TMPDIR 含空格等特殊字符。
	script := `do shell script "security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain " & quoted form of ` +
		applescriptString(certPath) +
		` with administrator privileges`

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// osascript 在用户取消密码时返回 "execution error: User canceled. (-128)",
		// 数字码不随语言变化。
		if strings.Contains(msg, "(-128)") || strings.Contains(strings.ToLower(msg), "user canceled") {
			return errors.New("已取消授权")
		}
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// applescriptString 把 s 转成 AppleScript 双引号字符串字面量,反斜杠与双引号需要转义。
func applescriptString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
