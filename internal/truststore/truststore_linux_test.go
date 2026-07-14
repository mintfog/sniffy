// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package truststore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// POSIX sh 单引号内不允许出现单引号,只能以闭合-转义-重开方式拼接。
func TestShellSingleQuoted(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/tmp/sniffy-ca.crt", "'/tmp/sniffy-ca.crt'"},
		{"", "''"},
		{"a'b", `'a'\''b'`},
		{"'", `''\'''`},
	}
	for _, tt := range tests {
		if got := shellSingleQuoted(tt.in); got != tt.want {
			t.Fatalf("shellSingleQuoted(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// 按声明顺序取第一个存在的锚目录;普通文件不算命中,全部缺失返回 false。
func TestPickDistroAnchor(t *testing.T) {
	orig := distroAnchors
	t.Cleanup(func() { distroAnchors = orig })

	base := t.TempDir()
	asFile := filepath.Join(base, "as-file")
	if err := os.WriteFile(asFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(base, "real-dir")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}

	distroAnchors = []distroAnchor{
		{filepath.Join(base, "missing"), "refresh-a"},
		{asFile, "refresh-b"},
		{realDir, "refresh-c"},
	}
	a, ok := pickDistroAnchor()
	if !ok || a.dir != realDir || a.refresh != "refresh-c" {
		t.Fatalf("pickDistroAnchor() = %+v, %v, want 命中 %q", a, ok, realDir)
	}

	distroAnchors = distroAnchors[:2]
	if a, ok := pickDistroAnchor(); ok {
		t.Fatalf("无有效目录时应返回 false,实际命中 %+v", a)
	}
}

// Chromium 的 ~/.pki/nssdb 不存在也要建出来;Firefox 侧只认含 cert9.db 的 profile 目录。
func TestCollectNSSDBs(t *testing.T) {
	home := t.TempDir()
	ffRoot := filepath.Join(home, ".mozilla", "firefox")
	withCert := filepath.Join(ffRoot, "abc.default-release")
	withoutCert := filepath.Join(ffRoot, "xyz.empty")
	for _, d := range []string{withCert, withoutCert} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(withCert, "cert9.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ffRoot, "profiles.ini"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	dbs := collectNSSDBs(home)

	chrome := filepath.Join(home, ".pki", "nssdb")
	if st, err := os.Stat(chrome); err != nil || !st.IsDir() {
		t.Fatalf("%s 应被自动创建: %v", chrome, err)
	}
	want := []string{chrome, withCert}
	if !slices.Equal(dbs, want) {
		t.Fatalf("collectNSSDBs() = %v, want %v", dbs, want)
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
		{"not authorized 视为取消", exitErr, "Error executing command as another user: Not authorized", "truststore:canceled"},
		{"超时", context.DeadlineExceeded, "", "truststore:timeout:60"},
		{"取消优先于超时", context.DeadlineExceeded, "Request dismissed", "truststore:canceled"},
		{"未识别输出保留原文并去首尾空白", exitErr, "  cp: cannot create regular file \n", "cp: cannot create regular file"},
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

// 用真 certutil 走通 NSS 同步全链路:空的 ~/.pki/nssdb 由 certutil -A 自动建库,
// 已初始化的 Firefox profile 同步写入,重复安装经先删后加不冲突。
func TestUpdateUserNSSDBsIntegration(t *testing.T) {
	certutilPath, err := exec.LookPath("certutil")
	if err != nil {
		t.Skip("需要 certutil(libnss3-tools)")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, ".mozilla", "firefox", "abc.default-release")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(certutilPath, "-N", "--empty-password", "-d", "sql:"+profile).CombinedOutput(); err != nil {
		t.Fatalf("初始化 NSS 库失败: %v: %s", err, out)
	}

	certPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(certPath, testCAPEM(t), 0o644); err != nil {
		t.Fatal(err)
	}

	verify := func() {
		t.Helper()
		for _, db := range []string{filepath.Join(home, ".pki", "nssdb"), profile} {
			if out, err := exec.Command(certutilPath, "-L", "-d", "sql:"+db, "-n", "Sniffy Root CA").CombinedOutput(); err != nil {
				t.Errorf("%s 中未找到证书: %v: %s", db, err, out)
			}
		}
	}
	updateUserNSSDBs(certPath)
	verify()
	updateUserNSSDBs(certPath)
	verify()
}

func testCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Sniffy Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
