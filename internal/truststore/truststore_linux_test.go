// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package truststore

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// POSIX sh 单引号内不允许出现单引号,只能以闭合-转义-重开方式拼接。
func TestShellSingleQuoted(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/tmp/sniffy-ca.crt", "'/tmp/sniffy-ca.crt'"},
		{"", "''"},
		{"a'b", `'a'\''b'`},
		{"'", `''\'''`},
		{"''", `''\'''\'''`},
	}
	for _, tt := range tests {
		if got := shellSingleQuoted(tt.in); got != tt.want {
			t.Fatalf("shellSingleQuoted(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestShellSingleQuotedRoundTripThroughShell(t *testing.T) {
	tests := []string{
		"/tmp/sniffy-ca.crt",
		"",
		"a'b",
		"''",
		"it's a 'test'",
		"多字节 '路径'/证书.crt",
		"$HOME `id` $(id) \\ \" ;",
		"换行\n制表\t",
	}
	for _, in := range tests {
		script := "printf '%s' " + shellSingleQuoted(in)
		out := runCommand(t, "sh", "-c", script)
		if out != in {
			t.Errorf("sh 还原 shellSingleQuoted(%q) = %q", in, out)
		}
	}
}

// 按声明顺序取第一个存在的锚目录;普通文件不算命中,全部缺失返回 false。
func TestPickDistroAnchor(t *testing.T) {
	base := t.TempDir()
	asFile := writeFile(t, filepath.Join(base, "as-file"), nil)
	realDir := mkdirAll(t, filepath.Join(base, "real-dir"), 0o755)

	setDistroAnchors(t, []distroAnchor{
		{dir: filepath.Join(base, "missing"), refresh: "refresh-a"},
		{dir: asFile, refresh: "refresh-b"},
		{dir: realDir, refresh: "refresh-c"},
	})

	a, ok := pickDistroAnchor()
	if !ok || a.dir != realDir || a.refresh != "refresh-c" {
		t.Fatalf("pickDistroAnchor() = %+v, %v, want 命中 %q", a, ok, realDir)
	}

	setDistroAnchors(t, []distroAnchor{
		{dir: filepath.Join(base, "missing"), refresh: "refresh-a"},
		{dir: asFile, refresh: "refresh-b"},
	})
	if a, ok := pickDistroAnchor(); ok {
		t.Fatalf("无有效目录时应返回 false,实际命中 %+v", a)
	}
}

// 内置锚表是发行版适配的唯一入口,漏写或写坏一个目录会让该发行版整体不可用。
func TestDistroAnchorsTableIsWellFormed(t *testing.T) {
	if len(distroAnchors) == 0 {
		t.Fatal("锚表为空")
	}
	seen := map[string]bool{}
	for i, a := range distroAnchors {
		if !filepath.IsAbs(a.dir) {
			t.Errorf("第 %d 项目录 %q 不是绝对路径", i, a.dir)
		}
		if seen[a.dir] {
			t.Errorf("目录 %q 重复", a.dir)
		}
		seen[a.dir] = true
		if !strings.HasPrefix(a.refresh, "update-ca-") {
			t.Errorf("第 %d 项重建命令 %q 不像 CA 重建工具", i, a.refresh)
		}
	}
}

// Chromium 的 ~/.pki/nssdb 不存在也要建出来;Firefox 侧只认含 cert9.db 的 profile 目录。
func TestCollectNSSDBs(t *testing.T) {
	home := t.TempDir()
	chrome := filepath.Join(home, ".pki", "nssdb")
	ffRoot := filepath.Join(home, ".mozilla", "firefox")
	// 名字按字典序给出,断言输出顺序稳定(便于日志与调试复现)。
	first := mkdirAll(t, filepath.Join(ffRoot, "aaa.default-release"), 0o755)
	second := mkdirAll(t, filepath.Join(ffRoot, "bbb.dev-edition"), 0o755)
	empty := mkdirAll(t, filepath.Join(ffRoot, "ccc.empty"), 0o755)
	writeFile(t, filepath.Join(first, "cert9.db"), nil)
	writeFile(t, filepath.Join(second, "cert9.db"), []byte("sqlite"))
	writeFile(t, filepath.Join(empty, "cert8.db"), nil)
	writeFile(t, filepath.Join(ffRoot, "profiles.ini"), nil)

	want := []string{chrome, first, second}
	if got := collectNSSDBs(home); !slices.Equal(got, want) {
		t.Fatalf("collectNSSDBs() = %v, want %v", got, want)
	}
	if st, err := os.Stat(chrome); err != nil || !st.IsDir() {
		t.Fatalf("Chromium 数据库目录未创建: %v", err)
	}
}

// ~/.mozilla/firefox 是普通文件时按「没有 Firefox」处理,不影响 Chromium 侧。
func TestCollectNSSDBsFirefoxRootIsFile(t *testing.T) {
	home := t.TempDir()
	chrome := mkdirAll(t, filepath.Join(home, ".pki", "nssdb"), 0o700)
	writeFile(t, filepath.Join(home, ".mozilla", "firefox"), nil)

	if got := collectNSSDBs(home); !slices.Equal(got, []string{chrome}) {
		t.Fatalf("collectNSSDBs() = %v, want 仅 %v", got, chrome)
	}
}

// ~/.pki 下已有同名普通文件时无法建目录,应跳过而不是把文件当数据库。
func TestCollectNSSDBsChromePathBlocked(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".pki", "nssdb"), nil)

	if got := collectNSSDBs(home); len(got) != 0 {
		t.Fatalf("collectNSSDBs() = %v, want 空", got)
	}
}

func TestUpdateUserNSSDBsSkipsWithoutCertutil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	certPath := writeFile(t, filepath.Join(t.TempDir(), "ca.pem"), testCertPEM(t))
	updateUserNSSDBs(certPath)

	if _, err := os.Stat(filepath.Join(home, ".pki")); !os.IsNotExist(err) {
		t.Fatalf("certutil 缺失时不应改动用户目录: %v", err)
	}
}

// 校验对 pkexec 输出中稳定锚点(C locale 下)的识别与分支优先级:
// 用户取消 > 超时;未识别输出保留原文以免丢失线索。
func TestInterpretPkexecErr(t *testing.T) {
	exitErr := errors.New("exit status 127")
	tests := []struct {
		name   string
		runErr error
		out    string
		want   string
	}{
		{"request dismissed 视为取消", exitErr, "Error executing command as another user: Request dismissed", "truststore:canceled"},
		{"大小写与前后缀不敏感", exitErr, "  REQUEST DISMISSED\n", "truststore:canceled"},
		{"not authorized 视为取消", exitErr, "Error executing command as another user: Not authorized", "truststore:canceled"},
		{"取消优先于超时", context.DeadlineExceeded, "Request dismissed", "truststore:canceled"},
		{"超时", context.DeadlineExceeded, "", "truststore:timeout:60"},
		{"未识别输出保留原文并去首尾空白", exitErr, "  cp: cannot create regular file \n", "cp: cannot create regular file"},
		{"多行输出只去首尾空白", exitErr, "\nfirst line\nsecond line\n", "first line\nsecond line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpretPkexecErr(tt.runErr, []byte(tt.out))
			if got == nil || got.Error() != tt.want {
				t.Fatalf("interpretPkexecErr(%v, %q) = %v, want %q", tt.runErr, tt.out, got, tt.want)
			}
		})
	}
}

// 输出为空且非超时时原样返回运行错误,保留错误链。
func TestInterpretPkexecErrEmptyOutput(t *testing.T) {
	runErr := errors.New("exit status 1")
	if got := interpretPkexecErr(runErr, nil); !errors.Is(got, runErr) {
		t.Fatalf("interpretPkexecErr(runErr, nil) = %v, want 原错误", got)
	}
}

func TestInstallWithoutPkexec(t *testing.T) {
	anchor := mkdirAll(t, filepath.Join(t.TempDir(), "anchors"), 0o755)
	setDistroAnchors(t, []distroAnchor{{dir: anchor, refresh: "true"}})
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	// 临时目录不可用时仍应优先报告 pkexec 缺失。
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))

	if err := Install(testCertPEM(t)); err == nil || err.Error() != "truststore:"+CodePkexecMissing {
		t.Fatalf("Install() = %v, want %s", err, CodePkexecMissing)
	}
	entries, err := os.ReadDir(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("前置检查失败仍改动了锚目录: %v", entries)
	}
}

// 全部锚目录缺失时在提权之前就给出 unsupported_distro。
func TestInstallNoSupportedDistro(t *testing.T) {
	base := t.TempDir()
	setDistroAnchors(t, []distroAnchor{{dir: filepath.Join(base, "missing"), refresh: "true"}})

	stubPkexec(t, `exit 1`)
	err := Install(testCertPEM(t))
	if err == nil || err.Error() != "truststore:"+CodeUnsupportedDistro {
		t.Fatalf("Install() = %v, want %s", err, CodeUnsupportedDistro)
	}
}

// 锚目录逐项尝试:第一个存在即用,后续项不参与命令构造。
func TestInstallUsesFirstExistingAnchor(t *testing.T) {
	base := t.TempDir()
	first := mkdirAll(t, filepath.Join(base, "first"), 0o755)
	second := mkdirAll(t, filepath.Join(base, "second"), 0o755)
	setDistroAnchors(t, []distroAnchor{
		{dir: filepath.Join(base, "missing"), refresh: "true"},
		{dir: first, refresh: "true"},
		{dir: second, refresh: "true"},
	})

	state := stubPkexec(t, `printf '%s' "$3" > "$STATE/script"; exit 0`)
	if err := Install(testCertPEM(t)); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	got := readState(t, state, "script")
	if !strings.Contains(got, shellSingleQuoted(first)) {
		t.Errorf("脚本未使用首个存在的锚目录:\n%s", got)
	}
	if strings.Contains(got, shellSingleQuoted(second)) {
		t.Errorf("脚本混入了后续锚目录:\n%s", got)
	}
}

func TestInstallCopiesCertIntoAnchor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []byte
	}{
		{name: "首次安装"},
		{name: "覆盖旧证书", existing: []byte("旧证书")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := mkdirAll(t, filepath.Join(t.TempDir(), "anchors"), 0o755)
			setDistroAnchors(t, []distroAnchor{{dir: anchor, refresh: "true"}})
			stubPkexec(t, `sh -c "$3"`)
			certPath := filepath.Join(anchor, "sniffy-ca.crt")
			if tc.existing != nil {
				writeFile(t, certPath, tc.existing)
			}

			certPEM := testCertPEM(t)
			if err := Install(certPEM); err != nil {
				t.Fatalf("Install() error: %v", err)
			}
			installed, err := os.ReadFile(certPath)
			if err != nil {
				t.Fatalf("锚目录中未找到证书: %v", err)
			}
			if !bytes.Equal(installed, certPEM) {
				t.Error("安装到锚目录的证书与输入不一致")
			}
			entries, err := os.ReadDir(anchor)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("锚目录内容 = %v, want 仅一份证书", entries)
			}
		})
	}
}

func TestInstallTempCertLifecycle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	anchor := mkdirAll(t, filepath.Join(t.TempDir(), "anchors"), 0o755)
	setDistroAnchors(t, []distroAnchor{{dir: anchor, refresh: "true"}})
	// 在脚本真正执行的那一刻快照临时目录,才能验证存在期间的权限与内容。
	state := stubPkexec(t, `stat -c '%a' "$TMPDIR"/sniffy-ca-* > "$STATE/mode"; ls -A "$TMPDIR"/sniffy-ca-* > "$STATE/entries"; sh -c "$3"`)

	if err := Install(testCertPEM(t)); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	if mode := strings.TrimSpace(readState(t, state, "mode")); mode != "700" {
		t.Errorf("临时目录权限 = %q, want 700", mode)
	}
	if entries := strings.Fields(readState(t, state, "entries")); !slices.Equal(entries, []string{"sniffy-ca.crt"}) {
		t.Errorf("临时目录内容 = %v, want 仅 sniffy-ca.crt", entries)
	}

	left, err := filepath.Glob(filepath.Join(tmp, "sniffy-ca-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("安装结束后残留临时目录: %v", left)
	}
}

// pkexec 的失败输出必须经归类后返回,而不是被丢弃成笼统的错误。
func TestInstallInterpretsPkexecFailure(t *testing.T) {
	anchor := mkdirAll(t, filepath.Join(t.TempDir(), "anchors"), 0o755)
	setDistroAnchors(t, []distroAnchor{{dir: anchor, refresh: "true"}})
	stubPkexec(t, `echo 'Error executing command as another user: Request dismissed' >&2; exit 126`)

	if err := Install(testCertPEM(t)); err == nil || err.Error() != "truststore:"+CodeCanceled {
		t.Fatalf("Install() = %v, want %s", err, CodeCanceled)
	}
	entries, err := os.ReadDir(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("取消后锚目录不应有写入: %v", entries)
	}
}

// 重建命令失败时以原文透传,保留发行版工具给出的排查线索。
func TestInstallPropagatesRefreshFailure(t *testing.T) {
	anchor := mkdirAll(t, filepath.Join(t.TempDir(), "anchors"), 0o755)
	setDistroAnchors(t, []distroAnchor{{dir: anchor, refresh: "echo refresh-failed >&2; false"}})
	stubPkexec(t, `sh -c "$3"`)

	err := Install(testCertPEM(t))
	if err == nil || !strings.Contains(err.Error(), "refresh-failed") {
		t.Fatalf("Install() = %v, want 含 refresh-failed 原文", err)
	}
}

// 用真 certutil 走通 NSS 同步全链路:空的 ~/.pki/nssdb 由 certutil -A 自动建库,
// 已初始化的 Firefox profile 同步写入,重复安装经先删后加不冲突。
func TestUpdateUserNSSDBsIntegration(t *testing.T) {
	certutilPath, err := exec.LookPath("certutil")
	if err != nil {
		t.Skip("需要 certutil(libnss3-tools)")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := mkdirAll(t, filepath.Join(home, ".mozilla", "firefox", "abc.default-release"), 0o700)
	runCommand(t, certutilPath, "-N", "--empty-password", "-d", "sql:"+profile)

	pemBytes := testCertPEM(t)
	certPath := writeFile(t, filepath.Join(t.TempDir(), "ca.pem"), pemBytes)
	certDER := parseCertPEM(t, pemBytes).Raw

	verify := func(stage string, wantDER []byte) {
		t.Helper()
		for _, db := range []string{filepath.Join(home, ".pki", "nssdb"), profile} {
			out := runCommand(t, certutilPath, "-L", "-d", "sql:"+db)
			// nickname 固定,重复安装靠先删后加保持唯一;-t "C,," 只给 SSL 信任位。
			matches := 0
			for _, line := range strings.Split(out, "\n") {
				if !strings.Contains(line, "Sniffy Root CA") {
					continue
				}
				matches++
				if !strings.Contains(line, "C,,") {
					t.Errorf("%s:%s 信任位不是 C,,: %q", stage, db, line)
				}
			}
			if matches != 1 {
				t.Errorf("%s:%s 中 Sniffy Root CA 出现 %d 次, want 1:\n%s", stage, db, matches, out)
				continue
			}
			if got := exportNSSDBCert(t, certutilPath, db); !bytes.Equal(got, wantDER) {
				t.Errorf("%s:%s 中的证书不是当前待装的那张", stage, db)
			}
		}
	}

	updateUserNSSDBs(certPath)
	verify("首次安装", certDER)

	updateUserNSSDBs(certPath)
	verify("重复安装", certDER)

	rotatedBytes := testCertPEM(t)
	rotated := writeFile(t, filepath.Join(t.TempDir(), "rotated.pem"), rotatedBytes)
	updateUserNSSDBs(rotated)
	verify("证书轮换", parseCertPEM(t, rotatedBytes).Raw)
}

// exportNSSDBCert 用 certutil 导出指定 nickname 的 DER 字节,便于比对库中实际是哪张证书。
func exportNSSDBCert(t *testing.T, certutilPath, db string) []byte {
	t.Helper()
	out := runCommand(t, certutilPath, "-L", "-d", "sql:"+db, "-n", "Sniffy Root CA", "-a")
	block, _ := pem.Decode([]byte(out))
	if block == nil {
		t.Fatalf("导出的内容不是 PEM:\n%s", out)
	}
	return block.Bytes
}

func setDistroAnchors(t *testing.T, anchors []distroAnchor) {
	t.Helper()
	original := distroAnchors
	distroAnchors = anchors
	t.Cleanup(func() { distroAnchors = original })
}

// stubPkexec 替换提权入口并隔离 HOME,返回状态目录。
// behavior 的 $3 是待执行脚本,$STATE 指向状态目录;原 PATH 供 sh 等工具使用。
func stubPkexec(t *testing.T, behavior string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // 隔离用户级 NSS 同步,避免写真实 HOME
	state := t.TempDir()
	t.Setenv("STATE", state)
	bin := t.TempDir()
	pkexec := writeFile(t, filepath.Join(bin, "pkexec"), []byte("#!/bin/sh\n"+behavior+"\n"))
	if err := os.Chmod(pkexec, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

func readState(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("读取状态文件 %s 失败: %v", name, err)
	}
	return string(data)
}
