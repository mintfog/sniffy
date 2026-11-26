// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package truststore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsTrustedSeesChangesWhileRunning：同一进程里先后装入、移除根证书，IsTrusted 每次都按
// 信任库此刻的内容判断，不沿用第一次读到的结果。
func TestIsTrustedSeesChangesWhileRunning(t *testing.T) {
	certPEM := testCertPEM(t)
	other := testCertPEM(t)
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle.pem")
	certs := mkdirAll(t, filepath.Join(dir, "certs"), 0o700)
	t.Setenv("SSL_CERT_FILE", bundle)
	t.Setenv("SSL_CERT_DIR", certs)

	check := func(want bool) {
		t.Helper()
		got, err := IsTrusted(certPEM)
		if err != nil || got != want {
			t.Fatalf("IsTrusted = %v, %v，期望 %v", got, err, want)
		}
	}
	writeFile(t, bundle, other)
	check(false)
	writeFile(t, bundle, append(other, certPEM...))
	check(true)
	writeFile(t, bundle, other)
	check(false)

	// 证书目录里的文件同样读取，按哈希命名、指向同目录文件的链接跳过不影响结果。
	writeFile(t, filepath.Join(certs, "sniffy.pem"), certPEM)
	if err := os.Symlink("sniffy.pem", filepath.Join(certs, "0123abcd.0")); err != nil {
		t.Fatal(err)
	}
	check(true)
}

// TestSystemRootsReportsUnreadableStore：信任库存在却一张证书都读不出来时返回错误，而不是
// 当作未信任。
func TestSystemRootsReportsUnreadableStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 读得了无权限的文件")
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle.pem")
	if err := os.WriteFile(bundle, testCertPEM(t), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", bundle)
	t.Setenv("SSL_CERT_DIR", filepath.Join(dir, "missing"))
	if _, err := IsTrusted(testCertPEM(t)); err == nil {
		t.Fatal("信任库不可读时应返回错误")
	}
}
