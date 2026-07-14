// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package truststore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// 序列化格式 "truststore:<code>[:<detail>]" 是与前端错误码解析的接口约定。
func TestErrorSerialization(t *testing.T) {
	if got := (&Error{Code: CodeCanceled}).Error(); got != "truststore:canceled" {
		t.Fatalf("无 Detail 序列化 = %q", got)
	}
	if got := (&Error{Code: CodeTimeout, Detail: "60"}).Error(); got != "truststore:timeout:60" {
		t.Fatalf("带 Detail 序列化 = %q", got)
	}
}

// 每次调用必须写入独享目录,并发安装互不覆盖;目录由调用方清理。
func TestWriteTempCert(t *testing.T) {
	pem := []byte("-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n")

	dir1, path1, err := writeTempCert(pem)
	if err != nil {
		t.Fatalf("writeTempCert() error: %v", err)
	}
	defer os.RemoveAll(dir1)

	if filepath.Dir(path1) != dir1 {
		t.Fatalf("证书路径 %q 不在返回目录 %q 下", path1, dir1)
	}
	got, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("读取写出的证书失败: %v", err)
	}
	if !bytes.Equal(got, pem) {
		t.Fatalf("写出内容不一致: %q", got)
	}

	dir2, _, err := writeTempCert(pem)
	if err != nil {
		t.Fatalf("第二次 writeTempCert() error: %v", err)
	}
	defer os.RemoveAll(dir2)
	if dir2 == dir1 {
		t.Fatalf("两次调用返回同一目录 %q", dir1)
	}
}

func TestWriteTempCertTempDirUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	// 覆盖各平台 os.TempDir 依赖的全部环境变量。
	for _, k := range []string{"TMPDIR", "TMP", "TEMP", "USERPROFILE"} {
		t.Setenv(k, missing)
	}
	if _, _, err := writeTempCert([]byte("x")); err == nil {
		t.Fatal("临时目录不可用时应返回错误")
	}
}
