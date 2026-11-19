// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package truststore

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
)

// IsTrusted 验证当前根证书是否受本机系统信任。Linux、Windows、macOS 上按系统此刻的信任库判断，
// 运行中才装入或移除的根证书同样反映出来，例外见 systemRoots。
func IsTrusted(certPEM []byte) (bool, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return false, errors.New("根证书 PEM 无效")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}
	roots, err := systemRoots()
	if err != nil {
		return false, err
	}
	return verifyTrust(cert, x509.VerifyOptions{Roots: roots})
}

func verifyTrust(cert *x509.Certificate, opts x509.VerifyOptions) (bool, error) {
	_, err := cert.Verify(opts)
	if err == nil {
		return true, nil
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return false, nil
	}
	return false, err
}
