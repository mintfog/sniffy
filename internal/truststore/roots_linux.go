// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package truststore

import (
	"crypto/x509"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// 与 crypto/x509 的 root_linux.go 相同：证书文件取第一个存在的，证书目录全部读取。
var (
	certFiles = []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/ca-bundle.pem",
		"/etc/pki/tls/cacert.pem",
		"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
		"/etc/ssl/cert.pem",
	}
	certDirectories = []string{
		"/etc/ssl/certs",
		"/etc/pki/tls/certs",
	}
)

// systemRoots 每次重新读取系统证书库。crypto/x509 在进程里第一次校验时读一遍就缓存下来，之后
// 才装入的根证书要到重启才看得见，而信任状态需要在运行中复查。读取的位置与 SSL_CERT_FILE、
// SSL_CERT_DIR 的含义都同 crypto/x509。
func systemRoots() (*x509.CertPool, error) {
	files := certFiles
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		files = []string{f}
	}
	dirs := certDirectories
	if d := os.Getenv("SSL_CERT_DIR"); d != "" {
		dirs = filepath.SplitList(d)
	}

	roots := x509.NewCertPool()
	loaded := false
	var firstErr error
	noteErr := func(err error) {
		if firstErr == nil && !errors.Is(err, fs.ErrNotExist) {
			firstErr = err
		}
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err == nil {
			loaded = roots.AppendCertsFromPEM(data)
			break
		}
		noteErr(err)
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			noteErr(err)
			continue
		}
		for _, e := range entries {
			if isSameDirSymlink(dir, e) {
				continue
			}
			if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				loaded = roots.AppendCertsFromPEM(data) || loaded
			}
		}
	}
	if !loaded && firstErr != nil {
		return nil, firstErr
	}
	return roots, nil
}

// isSameDirSymlink 报告 e 是否为指向同一目录内文件的符号链接。证书目录里按哈希命名的链接都
// 指向同目录的证书文件，跳过它们以免每张证书解析两遍。
func isSameDirSymlink(dir string, e fs.DirEntry) bool {
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(filepath.Join(dir, e.Name()))
	return err == nil && !strings.ContainsRune(target, filepath.Separator)
}
