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

// IsTrusted 验证当前根证书是否受本机系统信任。Linux 的系统根池由 Go 缓存，
// 此接口用于启动检测；进程内安装后的信任状态需在下次启动时重新读取。
func IsTrusted(certPEM []byte) (bool, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return false, errors.New("根证书 PEM 无效")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}
	return verifyTrust(cert, x509.VerifyOptions{})
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
