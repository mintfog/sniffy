// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

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

// installCanceledMarker 是 PS 端识别到 UAC 用户取消(ERROR_CANCELLED / 1223)后写到 stdout
// 的固定标记;Go 端按该字面量定位,不依赖会本地化的 Win32Exception.Message。
const installCanceledMarker = "__SNIFFY_CANCELED_BY_USER__"

// Install 通过 PowerShell 启动 certutil 并以 UAC 提权,把证书装入 LocalMachine 的「受信任的
// 根证书颁发机构」。Start-Process -Verb RunAs 在非管理员上下文里弹 UAC,用户同意才执行。
func Install(pem []byte) error {
	dir, certPath, err := writeTempCert(pem)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	psScript := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; try { `+
			`$p = Start-Process -FilePath 'certutil.exe' -ArgumentList @('-addstore','-f','Root',%s) -Verb RunAs -Wait -PassThru -WindowStyle Hidden; `+
			`if ($p.ExitCode -ne 0) { throw "certutil exited $($p.ExitCode)" } `+
			`} catch [System.ComponentModel.Win32Exception] { `+
			// 1223 = ERROR_CANCELLED,UAC 拒绝时抛出;透出标记跨语言识别。
			`if ($_.Exception.NativeErrorCode -eq 1223) { Write-Output '%s'; exit 2 } `+
			`throw `+
			`}`,
		psSingleQuoted(certPath),
		installCanceledMarker,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, installCanceledMarker) {
			return errors.New("已取消授权")
		}
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// psSingleQuoted 把 s 转成 PowerShell 单引号字符串字面量;单引号在内部用两个连续单引号转义。
func psSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
