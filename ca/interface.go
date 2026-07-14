// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package ca

import (
	"crypto/tls"
	"crypto/x509"
)

type CA interface {
	GetCA() *x509.Certificate
	// GetCAKey 返回根 CA 私钥,导出/签发时使用。类型为 crypto.Signer(ECDSA 或 RSA)。
	GetCAKey() any
	IssueCert(domain string) (*tls.Certificate, error)
}
