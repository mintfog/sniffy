// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package truststore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// distroAnchor 描述一个 Linux 发行版的「锚目录 + 重建命令」组合。
type distroAnchor struct {
	dir     string
	refresh string
}

// distroAnchors 按探测优先级列出支持的发行版路径,取第一个命中的目录。
var distroAnchors = []distroAnchor{
	{"/usr/local/share/ca-certificates", "update-ca-certificates"},           // Debian/Ubuntu
	{"/etc/pki/ca-trust/source/anchors", "update-ca-trust extract"},          // RHEL/Fedora/CentOS
	{"/etc/ca-certificates/trust-source/anchors", "update-ca-trust extract"}, // Arch/Manjaro
	{"/etc/pki/trust/anchors", "update-ca-certificates"},                     // openSUSE
}

// Install 经 pkexec 提权把根证书装入发行版 CA bundle,并顺带同步当前用户的 NSS 数据库
// (~/.pki/nssdb 与 Firefox 各 profile 下的 cert9.db),让 Chromium/Firefox 立即信任。
// NSS 同步是 best-effort,失败不回滚主流程。
func Install(pem []byte) error {
	if _, err := exec.LookPath("pkexec"); err != nil {
		return &Error{Code: CodePkexecMissing}
	}

	dir, certPath, err := writeTempCert(pem)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	anchor, ok := pickDistroAnchor()
	if !ok {
		return &Error{Code: CodeUnsupportedDistro}
	}

	// 固定文件名让重装以覆盖生效,旧条目自动失效。
	script := fmt.Sprintf(
		"cp %s %s/sniffy-ca.crt && %s",
		shellSingleQuoted(certPath),
		shellSingleQuoted(anchor.dir),
		anchor.refresh,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pkexec", "sh", "-c", script)
	// polkit 的取消提示("Request dismissed"/"Not authorized")随 LC_MESSAGES 走 gettext,
	// 锁回 C locale 保证下面的取消检测不依赖用户桌面语言。
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// 超时被杀时 CombinedOutput 只报进程被杀,以 ctx 错误为准。
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return interpretPkexecErr(err, out)
	}

	// Chromium/Firefox 不读 /etc/ssl/certs,只信各自 NSS 库,缺这一步浏览器会继续报
	// SEC_ERROR_UNKNOWN_ISSUER。
	updateUserNSSDBs(certPath)
	return nil
}

// interpretPkexecErr 把 pkexec 的失败归类为稳定错误码(*Error),无法识别的输出以原文透传。
// 文本锚点匹配依赖 Install 里锁定的 C locale。
func interpretPkexecErr(runErr error, out []byte) error {
	msg := strings.TrimSpace(string(out))
	low := strings.ToLower(msg)
	if strings.Contains(low, "request dismissed") || strings.Contains(low, "not authorized") {
		return &Error{Code: CodeCanceled}
	}
	if errors.Is(runErr, context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, Detail: "60"}
	}
	if msg == "" {
		return runErr
	}
	return errors.New(msg)
}

// pickDistroAnchor 选用第一个存在的发行版锚目录,均不存在返回 false。
func pickDistroAnchor() (distroAnchor, bool) {
	for _, a := range distroAnchors {
		if dirExists(a.dir) {
			return a, true
		}
	}
	return distroAnchor{}, false
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// shellSingleQuoted 单引号包裹字符串供 sh 用,内部单引号以「闭合-转义-重开」方式拼接。
func shellSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// updateUserNSSDBs 把根证书追加到当前用户拥有的 NSS 数据库:
//   - ~/.pki/nssdb (Chrome / Chromium / Edge)
//   - ~/.mozilla/firefox/<profile>/cert9.db (每个 Firefox profile)
//
// 全流程 best-effort:certutil 缺失、profile 被锁、目录权限异常均静默跳过。
func updateUserNSSDBs(certPath string) {
	if _, err := exec.LookPath("certutil"); err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, db := range collectNSSDBs(home) {
		addToNSSDB(db, certPath)
	}
}

// collectNSSDBs 列举要更新的 NSS 数据库目录(sql: 风格,目录路径)。Chromium 用的
// ~/.pki/nssdb 不存在时主动建出来,与 Chrome 首启行为一致。
func collectNSSDBs(home string) []string {
	var dbs []string
	chrome := filepath.Join(home, ".pki", "nssdb")
	if err := os.MkdirAll(chrome, 0o700); err == nil {
		dbs = append(dbs, chrome)
	}
	// Firefox 58+ 弃用了老格式 cert8.db,只识别 cert9.db。
	ffRoot := filepath.Join(home, ".mozilla", "firefox")
	if entries, err := os.ReadDir(ffRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(ffRoot, e.Name())
			if _, err := os.Stat(filepath.Join(p, "cert9.db")); err == nil {
				dbs = append(dbs, p)
			}
		}
	}
	return dbs
}

// addToNSSDB 用 certutil 把证书写入指定 NSS 数据库。先删后加避免同 nickname 冲突,
// 首次安装时删除步骤会以「未找到」失败,忽略。
// 每条命令 10s 超时:Firefox 打开时 cert9.db 可能被 NSS 独占锁,certutil 会无限阻塞。
func addToNSSDB(dbDir, certPath string) {
	const nick = "Sniffy Root CA"
	target := "sql:" + dbDir
	runNSS := func(args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "certutil", args...).Run()
	}
	runNSS("-D", "-d", target, "-n", nick)
	// -t "C,,":SSL trust=CA,其余信任位为空(只用作 TLS server 根)。
	runNSS("-A", "-d", target, "-t", "C,,", "-n", nick, "-i", certPath)
}
